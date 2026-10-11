package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
)

// Migrate applies db/migrations with goose. It runs as the database owner
// (postgres) from CI or vaultctl — never from the request-serving Lambdas.
func Migrate(ctx context.Context, ownerURL, dir string) error {
	db, err := sql.Open("pgx", ownerURL)
	if err != nil {
		return err
	}
	defer db.Close()
	// Keep goose's bookkeeping out of `public`, which Supabase exposes through
	// its Data API (core principle 4).
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS vaultops_meta;
		REVOKE ALL ON SCHEMA vaultops_meta FROM PUBLIC`); err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS(dir), goose.WithTableName("vaultops_meta.goose_db_version"))
	if err != nil {
		return fmt.Errorf("store: migrations: %w", err)
	}
	_, err = p.Up(ctx)
	return err
}

// SetAppRolePassword enables LOGIN for the runtime role. The password is
// supplied at deploy time and never stored in migrations.
func SetAppRolePassword(ctx context.Context, ownerURL, password string) error {
	if len(password) < 16 {
		return fmt.Errorf("store: app role password must be at least 16 characters")
	}
	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	// ALTER ROLE cannot take bind parameters; quote the literal safely.
	lit, err := quoteLiteral(ctx, conn, password)
	if err != nil {
		return err
	}
	_, err = conn.Exec(ctx, "ALTER ROLE vaultops_app LOGIN PASSWORD "+lit)
	return err
}

func quoteLiteral(ctx context.Context, conn *pgx.Conn, s string) (string, error) {
	var q string
	err := conn.QueryRow(ctx, "SELECT quote_literal($1)", s).Scan(&q)
	return q, err
}
