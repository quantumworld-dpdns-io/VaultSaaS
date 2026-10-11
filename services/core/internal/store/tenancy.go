package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrInvitationInvalid = errors.New("store: invitation invalid or expired")
	ErrInvitationEmail   = errors.New("store: invitation is for a different email")
	ErrConflict          = errors.New("store: conflict")
)

// ---- cross-tenant entry points (SECURITY DEFINER functions) -----------------

// MemberRole returns the user's role in org, or "" when not a member.
func (s *Store) MemberRole(ctx context.Context, org, user uuid.UUID) (string, error) {
	var role *string
	if err := s.pool.QueryRow(ctx, `SELECT app.member_role($1, $2)`, org, user).Scan(&role); err != nil {
		return "", err
	}
	if role == nil {
		return "", nil
	}
	return *role, nil
}

type Membership struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	Role string    `json:"role"`
}

func (s *Store) MyOrganizations(ctx context.Context, user uuid.UUID) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, role FROM app.my_organizations($1)`, user)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Membership, error) {
		var m Membership
		return m, r.Scan(&m.ID, &m.Name, &m.Role)
	})
}

// CreateOrganizationWithOwner creates a tenant whose first member is owner,
// returning the one-time webhook token.
func (s *Store) CreateOrganizationWithOwner(ctx context.Context, name string, owner uuid.UUID) (Organization, string, error) {
	token, err := NewWebhookToken()
	if err != nil {
		return Organization{}, "", err
	}
	var o Organization
	err = s.pool.QueryRow(ctx, `SELECT id, name FROM app.create_organization_with_owner($1, $2, $3)`,
		name, HashWebhookToken(token), owner).Scan(&o.ID, &o.Name)
	return o, token, err
}

func (s *Store) AcceptInvitation(ctx context.Context, token string, user uuid.UUID, email string) (uuid.UUID, string, error) {
	var org uuid.UUID
	var role string
	err := s.pool.QueryRow(ctx, `SELECT organization_id, role FROM app.accept_invitation($1, $2, $3)`,
		hashToken(token), user, email).Scan(&org, &role)
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "P0002":
			return org, "", ErrInvitationInvalid
		case "42501":
			return org, "", ErrInvitationEmail
		}
	}
	return org, role, err
}

// WalletOwner returns who has linked an address, or uuid.Nil.
func (s *Store) WalletOwner(ctx context.Context, chain, address string) (uuid.UUID, error) {
	var owner *uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT app.wallet_owner($1, $2)`, chain, address).Scan(&owner); err != nil {
		return uuid.Nil, err
	}
	if owner == nil {
		return uuid.Nil, nil
	}
	return *owner, nil
}

// VerifyAuditChain returns the id of the first broken audit row, or 0.
func (s *Store) VerifyAuditChain(ctx context.Context, org uuid.UUID) (int64, error) {
	var broken *int64
	if err := s.pool.QueryRow(ctx, `SELECT app.verify_audit_chain($1)`, org).Scan(&broken); err != nil {
		return 0, err
	}
	if broken == nil {
		return 0, nil
	}
	return *broken, nil
}

type AuditHead struct {
	OrganizationID uuid.UUID
	LastID         int64
	HeadHash       []byte
	Entries        int64
}

func (s *Store) AuditHeads(ctx context.Context) ([]AuditHead, error) {
	rows, err := s.pool.Query(ctx, `SELECT organization_id, last_id, head_hash, entries FROM app.audit_heads()`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditHead, error) {
		var h AuditHead
		return h, r.Scan(&h.OrganizationID, &h.LastID, &h.HeadHash, &h.Entries)
	})
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ---- org-scoped -------------------------------------------------------------

type Member struct {
	UserID    uuid.UUID `json:"userId"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
}

func (t *Tx) ListMembers() ([]Member, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT user_id, email, role, created_at FROM app.current_org_members()`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Member, error) {
		var m Member
		return m, r.Scan(&m.UserID, &m.Email, &m.Role, &m.CreatedAt)
	})
}

// CreateInvitation stores an invitation and returns its one-time token.
func (t *Tx) CreateInvitation(email, role string, invitedBy uuid.UUID, ttl time.Duration) (uuid.UUID, string, error) {
	token, err := NewWebhookToken()
	if err != nil {
		return uuid.Nil, "", err
	}
	var id uuid.UUID
	err = t.tx.QueryRow(t.ctx, `INSERT INTO app.invitations (organization_id, email, role, token_hash, invited_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		t.Org, email, role, hashToken(token), invitedBy, time.Now().Add(ttl)).Scan(&id)
	return id, token, err
}

type BitGoConnection struct {
	Environment    string
	EnterpriseID   string
	EncryptedToken []byte
	KeyVersion     int
	OwnerUserID    uuid.UUID
	VerifiedAt     time.Time
	TokenExpiresAt *time.Time
}

func (t *Tx) BitGoConnection() (*BitGoConnection, error) {
	var c BitGoConnection
	err := t.tx.QueryRow(t.ctx, `SELECT environment, enterprise_id, encrypted_token, key_version, owner_user_id, verified_at,
		token_expires_at FROM app.bitgo_connections WHERE organization_id = $1`, t.Org).
		Scan(&c.Environment, &c.EnterpriseID, &c.EncryptedToken, &c.KeyVersion, &c.OwnerUserID, &c.VerifiedAt, &c.TokenExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

func (t *Tx) PutBitGoConnection(c BitGoConnection) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO app.bitgo_connections
		(organization_id, environment, enterprise_id, encrypted_token, key_version, owner_user_id, verified_at, token_expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (organization_id) DO UPDATE SET environment = EXCLUDED.environment,
		  enterprise_id = EXCLUDED.enterprise_id, encrypted_token = EXCLUDED.encrypted_token,
		  key_version = EXCLUDED.key_version, owner_user_id = EXCLUDED.owner_user_id,
		  verified_at = EXCLUDED.verified_at, token_expires_at = EXCLUDED.token_expires_at, updated_at = now()`,
		t.Org, c.Environment, c.EnterpriseID, c.EncryptedToken, c.KeyVersion, c.OwnerUserID, c.VerifiedAt, c.TokenExpiresAt)
	return err
}

// ---- user-scoped (linked wallets, D4) ----------------------------------------

func (t *Tx) CreateSiweNonce(nonce string, ttl time.Duration) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO app.siwe_nonces (nonce, user_id, expires_at) VALUES ($1, $2, $3)`,
		nonce, t.User, time.Now().Add(ttl))
	return err
}

// ConsumeSiweNonce marks the caller's nonce used; false if unknown, used or expired.
func (t *Tx) ConsumeSiweNonce(nonce string) (bool, error) {
	ct, err := t.tx.Exec(t.ctx, `UPDATE app.siwe_nonces SET used_at = now()
		WHERE nonce = $1 AND user_id = $2 AND used_at IS NULL AND expires_at > now()`, nonce, t.User)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

func (t *Tx) LinkWallet(chain, address string) error {
	_, err := t.tx.Exec(t.ctx, `INSERT INTO app.user_wallets (user_id, chain, address) VALUES ($1, $2, $3)`, t.User, chain, address)
	if isUniqueViolation(err) {
		return ErrConflict
	}
	return err
}

type LinkedWallet struct {
	Chain      string    `json:"chain"`
	Address    string    `json:"address"`
	VerifiedAt time.Time `json:"verifiedAt"`
}

func (t *Tx) ListLinkedWallets() ([]LinkedWallet, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT chain, address, verified_at FROM app.user_wallets WHERE user_id = $1 ORDER BY verified_at`, t.User)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (LinkedWallet, error) {
		var w LinkedWallet
		return w, r.Scan(&w.Chain, &w.Address, &w.VerifiedAt)
	})
}

// AuditHash returns the stored hash of one audit row in the current org.
func (t *Tx) AuditHash(id int64) ([]byte, error) {
	var h []byte
	err := t.tx.QueryRow(t.ctx, `SELECT hash FROM app.audit_logs WHERE id = $1`, id).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return h, err
}
