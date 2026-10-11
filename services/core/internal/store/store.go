// Package store is the Postgres (Supabase) persistence layer. All tenant
// access goes through InOrg, which runs a transaction with app.org_id set so
// row-level security applies (core principle 3).
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("store: not found")

type Store struct {
	pool *pgxpool.Pool
}

// Open connects using the Supavisor transaction-mode pooler URL in production.
// Transaction pooling cannot keep named prepared statements across
// transactions, so the exec mode avoids them.
func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("store: parse url: %w", err)
	}
	cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	cfg.MaxConns = 4 // Lambda: one request at a time per instance
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

// Tx is a transaction bound to a session context: an organization (for
// tenant tables) and/or a user (for per-user tables such as linked wallets).
type Tx struct {
	ctx  context.Context
	tx   pgx.Tx
	Org  uuid.UUID
	User uuid.UUID
}

// InOrg runs fn in a transaction bound to one organization.
func (s *Store) InOrg(ctx context.Context, org uuid.UUID, fn func(*Tx) error) error {
	return s.inSession(ctx, org, uuid.Nil, fn)
}

// InOrgAs is InOrg with the acting user recorded in the session as well.
func (s *Store) InOrgAs(ctx context.Context, org, user uuid.UUID, fn func(*Tx) error) error {
	return s.inSession(ctx, org, user, fn)
}

// AsUser runs fn bound to a user only (no tenant data is visible).
func (s *Store) AsUser(ctx context.Context, user uuid.UUID, fn func(*Tx) error) error {
	return s.inSession(ctx, uuid.Nil, user, fn)
}

func (s *Store) inSession(ctx context.Context, org, user uuid.UUID, fn func(*Tx) error) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.org_id', $1, true), set_config('app.user_id', $2, true)",
			uuidOrEmpty(org), uuidOrEmpty(user)); err != nil {
			return err
		}
		return fn(&Tx{ctx: ctx, tx: tx, Org: org, User: user})
	})
}

func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// ---- organizations (cross-tenant lookups) ---------------------------------

type Organization struct {
	ID   uuid.UUID
	Name string
}

// HashWebhookToken returns the stored form of a webhook path token.
func HashWebhookToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewWebhookToken returns a 256-bit URL-safe random token (43 chars).
func NewWebhookToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CreateOrganization creates a tenant and returns its one-time webhook token.
func (s *Store) CreateOrganization(ctx context.Context, name string) (Organization, string, error) {
	token, err := NewWebhookToken()
	if err != nil {
		return Organization{}, "", err
	}
	var o Organization
	err = s.pool.QueryRow(ctx, `SELECT id, name FROM app.create_organization($1, $2)`, name, HashWebhookToken(token)).Scan(&o.ID, &o.Name)
	return o, token, err
}

// OrganizationByWebhookToken resolves the tenant for an incoming webhook.
// It runs before any org context exists, through a narrow SECURITY DEFINER
// function rather than a broad RLS exception.
func (s *Store) OrganizationByWebhookToken(ctx context.Context, token string) (Organization, error) {
	var o Organization
	err := s.pool.QueryRow(ctx, `SELECT id, name FROM app.organization_by_webhook_token($1)`, HashWebhookToken(token)).Scan(&o.ID, &o.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}
