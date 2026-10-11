package store_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

// Tables that are not tenant-scoped by organization_id.
var nonTenantTables = map[string]string{
	"organizations": "scoped by id = current_org()",
	"user_wallets":  "scoped by user",
	"siwe_nonces":   "scoped by user",
}

// Core principle 3: every table in schema app has RLS enabled, and every
// tenant table carries organization_id.
func TestEveryTenantTableHasRLS(t *testing.T) {
	_, owner := storetest.New(t)
	ctx := context.Background()
	c := storetest.Owner(t, owner)

	rows, err := c.Query(ctx, `
		SELECT c.relname, c.relrowsecurity,
		       EXISTS (SELECT 1 FROM information_schema.columns col
		               WHERE col.table_schema = 'app' AND col.table_name = c.relname AND col.column_name = 'organization_id'
		                 AND col.is_nullable = 'NO')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'app' AND c.relkind = 'r' ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name string
		var rls, hasOrg bool
		if err := rows.Scan(&name, &rls, &hasOrg); err != nil {
			t.Fatal(err)
		}
		seen++
		if !rls {
			t.Errorf("app.%s: row-level security is not enabled", name)
		}
		if _, ok := nonTenantTables[name]; !ok && !hasOrg {
			t.Errorf("app.%s: tenant table without NOT NULL organization_id", name)
		}
	}
	if seen < 10 {
		t.Fatalf("expected the full app schema, saw %d tables", seen)
	}

	var super, bypass bool
	if err := c.QueryRow(ctx, `SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname = 'vaultops_app'`).Scan(&super, &bypass); err != nil {
		t.Fatal(err)
	}
	if super || bypass {
		t.Fatal("vaultops_app must not be superuser or BYPASSRLS")
	}
	// Core principle 4: Supabase API roles get nothing in schema app.
	for _, role := range []string{"anon", "authenticated"} {
		var usage bool
		c.QueryRow(ctx, `SELECT has_schema_privilege($1, 'app', 'USAGE')`, role).Scan(&usage)
		if usage {
			t.Errorf("%s must not have USAGE on schema app", role)
		}
	}
}

func seedOrgWithTransfer(t *testing.T, s *store.Store, owner, name string) (store.Organization, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	u := storetest.CreateUser(t, owner, strings.ToLower(name)+"@example.com")
	org, _, err := s.CreateOrganizationWithOwner(ctx, name, u)
	if err != nil {
		t.Fatal(err)
	}
	err = s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
		wid, err := tx.UpsertWallet(bitgo.Wallet{ID: "w-" + name, Coin: "tbtc4"})
		if err != nil {
			return err
		}
		obs, _ := domain.FromProvider(org.ID, wid, &bitgo.Transfer{ID: "t-" + name, Coin: "tbtc4", Type: "receive", ValueString: "1", State: "confirmed", Date: time.Now()})
		_, err = domain.ApplyObservation(tx, "test", obs)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return org, u
}

// BOLA/IDOR at the database layer: another tenant's rows are invisible and
// cannot be written, whatever the query says.
func TestCrossTenantRowsAreInvisible(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	a, _ := seedOrgWithTransfer(t, s, owner, "Alpha")
	b, _ := seedOrgWithTransfer(t, s, owner, "Beta")

	err := s.InOrg(ctx, a.ID, func(tx *store.Tx) error {
		var all, theirs, orgs int
		tx.QueryRowForTest(`SELECT count(*) FROM app.transfers`).Scan(&all)
		tx.QueryRowForTest(`SELECT count(*) FROM app.transfers WHERE organization_id = $1`, b.ID).Scan(&theirs)
		tx.QueryRowForTest(`SELECT count(*) FROM app.organizations`).Scan(&orgs)
		if all != 1 || theirs != 0 || orgs != 1 {
			t.Errorf("alpha sees transfers=%d beta-transfers=%d orgs=%d", all, theirs, orgs)
		}
		ct, err := tx.ExecForTest(`UPDATE app.transfers SET state = 'failed' WHERE organization_id = $1`, b.ID)
		if err != nil || ct != 0 {
			t.Errorf("cross-tenant update touched %d rows (err %v)", ct, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Deleting another tenant's wallets: refused by grants or filtered by RLS.
	_ = s.InOrg(ctx, a.ID, func(tx *store.Tx) error {
		ct, err := tx.ExecForTest(`DELETE FROM app.wallets WHERE organization_id = $1`, b.ID)
		if err == nil && ct != 0 {
			t.Errorf("cross-tenant delete touched %d rows", ct)
		}
		return err
	})
	// Writing a row that claims the other tenant violates WITH CHECK.
	err = s.InOrg(ctx, a.ID, func(tx *store.Tx) error {
		_, err := tx.ExecForTest(`INSERT INTO app.security_alerts (organization_id, kind, severity, message) VALUES ($1, 'x', 'low', 'x')`, b.ID)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "row-level security") {
		t.Fatalf("cross-tenant insert must violate RLS, got %v", err)
	}
	// No org context: nothing is visible.
	s.AsUser(ctx, uuid.New(), func(tx *store.Tx) error {
		var n int
		tx.QueryRowForTest(`SELECT count(*) FROM app.transfers`).Scan(&n)
		if n != 0 {
			t.Errorf("session without org sees %d transfers", n)
		}
		return nil
	})
}

func TestAuditLogIsAppendOnlyAndTamperEvident(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	org, _ := seedOrgWithTransfer(t, s, owner, "Gamma")
	for i := 0; i < 3; i++ {
		s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
			return tx.AppendAudit(org.ID, "test", "test.event", "thing", "1", map[string]any{"i": i})
		})
	}
	if broken, err := s.VerifyAuditChain(ctx, org.ID); err != nil || broken != 0 {
		t.Fatalf("fresh chain should verify, broken=%d err=%v", broken, err)
	}

	c := storetest.Owner(t, owner)
	for _, stmt := range []string{
		`UPDATE app.audit_logs SET action = 'forged'`,
		`DELETE FROM app.audit_logs`,
		`TRUNCATE app.audit_logs`,
	} {
		if _, err := c.Exec(ctx, stmt); err == nil || !strings.Contains(err.Error(), "append-only") {
			t.Errorf("%q must be rejected by the trigger, got %v", stmt, err)
		}
	}
	// The runtime role cannot even attempt it.
	s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
		if _, err := tx.ExecForTest(`UPDATE app.audit_logs SET action = 'x'`); err == nil {
			t.Error("vaultops_app must not be able to UPDATE audit_logs")
		}
		return nil
	})

	// Simulate an attacker with owner rights who disables the trigger.
	var victim int64
	c.QueryRow(ctx, `SELECT min(id) + 1 FROM app.audit_logs WHERE organization_id = $1`, org.ID).Scan(&victim)
	if _, err := c.Exec(ctx, `ALTER TABLE app.audit_logs DISABLE TRIGGER audit_no_update;
		UPDATE app.audit_logs SET data = '{"i": 999}' WHERE id = `+strconv.FormatInt(victim, 10)+`;
		ALTER TABLE app.audit_logs ENABLE TRIGGER audit_no_update`); err != nil {
		t.Fatal(err)
	}
	broken, err := s.VerifyAuditChain(ctx, org.ID)
	if err != nil || broken != victim {
		t.Fatalf("tampering with row %d must be detected, got broken=%d err=%v", victim, broken, err)
	}
}

func TestInvitationFlow(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	org, admin := seedOrgWithTransfer(t, s, owner, "Delta")
	bob := storetest.CreateUser(t, owner, "bob@example.com")
	eve := storetest.CreateUser(t, owner, "eve@example.com")

	var token string
	s.InOrgAs(ctx, org.ID, admin, func(tx *store.Tx) error {
		var err error
		_, token, err = tx.CreateInvitation("bob@example.com", "viewer", admin, time.Hour)
		return err
	})
	if _, _, err := s.AcceptInvitation(ctx, token, eve, "eve@example.com"); !errors.Is(err, store.ErrInvitationEmail) {
		t.Fatalf("someone else's invitation must be refused, got %v", err)
	}
	gotOrg, role, err := s.AcceptInvitation(ctx, token, bob, "Bob@Example.com")
	if err != nil || gotOrg != org.ID || role != "viewer" {
		t.Fatalf("accept: %v %s %v", gotOrg, role, err)
	}
	if _, _, err := s.AcceptInvitation(ctx, token, bob, "bob@example.com"); !errors.Is(err, store.ErrInvitationInvalid) {
		t.Fatalf("invitation must be single-use, got %v", err)
	}
	if r, _ := s.MemberRole(ctx, org.ID, bob); r != "viewer" {
		t.Fatalf("bob role %q", r)
	}
	if r, _ := s.MemberRole(ctx, org.ID, eve); r != "" {
		t.Fatalf("eve must not be a member, got %q", r)
	}
}

func TestLinkedWalletsAreUserScoped(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	u1 := storetest.CreateUser(t, owner, "u1@example.com")
	u2 := storetest.CreateUser(t, owner, "u2@example.com")
	addr := "0x" + strings.Repeat("ab", 20)
	if err := s.AsUser(ctx, u1, func(tx *store.Tx) error { return tx.LinkWallet("eip155", addr) }); err != nil {
		t.Fatal(err)
	}
	if err := s.AsUser(ctx, u2, func(tx *store.Tx) error { return tx.LinkWallet("eip155", addr) }); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("an address can belong to one user only, got %v", err)
	}
	s.AsUser(ctx, u2, func(tx *store.Tx) error {
		ws, _ := tx.ListLinkedWallets()
		if len(ws) != 0 {
			t.Errorf("u2 sees u1's wallets: %v", ws)
		}
		return nil
	})
	if who, _ := s.WalletOwner(ctx, "eip155", addr); who != u1 {
		t.Fatalf("owner lookup: %v", who)
	}
}
