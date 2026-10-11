// Package storetest creates throwaway Postgres databases for integration
// tests. Tests are skipped unless VAULTOPS_TEST_DATABASE_URL points at a
// server where the user may CREATE DATABASE (e.g. `make local-up`).
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vaultops/core/internal/store"
)

// appPassword sets the cluster-wide vaultops_app password. Suites that also
// run a live stack against the same server pass theirs so neither breaks.
var appPassword = func() string {
	if p := os.Getenv("VAULTOPS_TEST_APP_PASSWORD"); p != "" {
		return p
	}
	return "storetest-app-password"
}()

// RepoRoot returns the repository root (for db/migrations).
func RepoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "../../../../..")
}

// New returns a store connected as the RLS-bound runtime role on a fresh,
// fully migrated database, plus the owner URL for assertions that must see
// every row.
func New(t *testing.T) (*store.Store, string) {
	t.Helper()
	admin := os.Getenv("VAULTOPS_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("VAULTOPS_TEST_DATABASE_URL not set; skipping database test")
	}
	ctx := context.Background()
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "vaultops_test_" + hex.EncodeToString(b)

	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	// Roles are cluster-wide: serialise setup across parallel test packages.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(727001)"); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock(727001)") //nolint:errcheck
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), admin)
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})

	owner := withDB(t, admin, name, "")
	stub, err := os.ReadFile(filepath.Join(RepoRoot(), "db/local/00_supabase_stub.sql"))
	if err != nil {
		t.Fatal(err)
	}
	oc, err := pgx.Connect(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := oc.Exec(ctx, string(stub)); err != nil {
		t.Fatal(err)
	}
	oc.Close(ctx)
	if err := store.Migrate(ctx, owner, filepath.Join(RepoRoot(), "db/migrations")); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAppRolePassword(ctx, owner, appPassword); err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, withDB(t, admin, name, "vaultops_app"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, owner
}

func withDB(t *testing.T, raw, db, user string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + db
	if user != "" {
		u.User = url.UserPassword(user, appPassword)
	}
	return u.String()
}

// CreateUser inserts a Supabase auth user (as the owner) and returns its id.
func CreateUser(t *testing.T, ownerURL, email string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	c, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	var id uuid.UUID
	if err := c.QueryRow(ctx, `INSERT INTO auth.users (email) VALUES ($1) RETURNING id`, email).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Owner opens a connection as the database owner (bypasses RLS) for assertions.
func Owner(t *testing.T, ownerURL string) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(context.Background(), ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}
