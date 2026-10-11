// Package connections resolves the BitGo client for an organization from its
// encrypted connection row. Each tenant uses its own BitGo token, so one
// tenant can never see another tenant's wallets through VaultOps.
package connections

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/crypto"
	"github.com/vaultops/core/internal/store"
)

var ErrNotConnected = errors.New("connections: organization has no BitGo connection")

type Resolver struct {
	Store   *store.Store
	Keyring *crypto.Keyring
	BaseURL string
	// LocalFallback is used only in VAULTOPS_ENV=local when an org has no
	// connection (the config refuses a global token anywhere else).
	LocalFallback *bitgo.Client
}

func aad(org uuid.UUID) []byte { return []byte("bitgo-token:" + org.String()) }

// ForOrg returns a BitGo client authenticated with the org's own token.
func (r *Resolver) ForOrg(ctx context.Context, org uuid.UUID) (*bitgo.Client, error) {
	var conn *store.BitGoConnection
	if err := r.Store.InOrg(ctx, org, func(tx *store.Tx) error {
		var err error
		conn, err = tx.BitGoConnection()
		return err
	}); err != nil {
		return nil, err
	}
	if conn == nil {
		if r.LocalFallback != nil {
			return r.LocalFallback, nil
		}
		return nil, ErrNotConnected
	}
	if r.Keyring == nil {
		return nil, errors.New("connections: TOKEN_ENC_KEYS not configured")
	}
	token, err := r.Keyring.Open(conn.EncryptedToken, conn.KeyVersion, aad(org))
	if err != nil {
		return nil, err
	}
	return bitgo.New(r.BaseURL, string(token)), nil
}

// Put verifies the token with a read-only call, then stores it sealed.
func (r *Resolver) Put(ctx context.Context, org, actor uuid.UUID, token, enterpriseID string, expiresAt *time.Time) (int, error) {
	if r.Keyring == nil {
		return 0, errors.New("connections: TOKEN_ENC_KEYS not configured")
	}
	wallets, err := bitgo.New(r.BaseURL, token, bitgo.WithMaxRetries(1)).ListWallets(ctx)
	if err != nil {
		return 0, err
	}
	sealed, ver, err := r.Keyring.Seal([]byte(token), aad(org))
	if err != nil {
		return 0, err
	}
	env := "test"
	if r.BaseURL == "https://app.bitgo.com" {
		env = "prod"
	}
	err = r.Store.InOrgAs(ctx, org, actor, func(tx *store.Tx) error {
		if err := tx.PutBitGoConnection(store.BitGoConnection{
			Environment: env, EnterpriseID: enterpriseID, EncryptedToken: sealed, KeyVersion: ver,
			OwnerUserID: actor, VerifiedAt: time.Now(), TokenExpiresAt: expiresAt,
		}); err != nil {
			return err
		}
		return tx.AppendAudit(org, "user:"+actor.String(), "bitgo_connection.updated", "organization", org.String(),
			map[string]any{"environment": env, "enterprise_id": enterpriseID, "wallets_visible": len(wallets), "key_version": ver,
				"token_expires_at": expiresAt})
	})
	return len(wallets), err
}
