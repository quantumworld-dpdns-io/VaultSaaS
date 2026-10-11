package store

import (
	"encoding/json"
	"errors"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
)

var _ domain.Ledger = (*Tx)(nil)

// ---- wallets ------------------------------------------------------------------

type Wallet struct {
	ID               uuid.UUID
	ProviderWalletID string
	Coin             string
}

func (t *Tx) WalletByProviderID(providerWalletID string) (*Wallet, error) {
	w := Wallet{}
	err := t.tx.QueryRow(t.ctx, `SELECT id, provider_wallet_id, coin FROM app.wallets
		WHERE organization_id = $1 AND provider = 'bitgo' AND provider_wallet_id = $2`, t.Org, providerWalletID).
		Scan(&w.ID, &w.ProviderWalletID, &w.Coin)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &w, err
}

// UpsertWallet records a BitGo wallet and its latest balances.
func (t *Tx) UpsertWallet(w bitgo.Wallet) (uuid.UUID, error) {
	var id uuid.UUID
	err := t.tx.QueryRow(t.ctx, `
		INSERT INTO app.wallets (organization_id, provider_wallet_id, coin, label, wallet_type,
		                         balance, confirmed_balance, spendable_balance, last_synced_at)
		VALUES ($1, $2, $3, $4, $5, $6::numeric, $7::numeric, $8::numeric, now())
		ON CONFLICT (organization_id, provider, provider_wallet_id) DO UPDATE SET
		  label = EXCLUDED.label, wallet_type = EXCLUDED.wallet_type, balance = EXCLUDED.balance,
		  confirmed_balance = EXCLUDED.confirmed_balance, spendable_balance = EXCLUDED.spendable_balance,
		  last_synced_at = now()
		RETURNING id`,
		t.Org, w.ID, w.Coin, w.Label, w.Type, nullNum(w.BalanceString), nullNum(w.ConfirmedBalanceString), nullNum(w.SpendableBalanceString),
	).Scan(&id)
	return id, err
}

func nullNum(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---- transfers (domain.Ledger) -------------------------------------------------

const transferCols = `id, organization_id, wallet_id, provider_transfer_id, coin, txid, direction,
	amount::text, receive_address, state, provider_state, confirmations, block_height, occurred_at, version`

func scanTransfer(row pgx.Row) (*domain.Transfer, error) {
	var tr domain.Transfer
	var amount, state string
	err := row.Scan(&tr.ID, &tr.OrganizationID, &tr.WalletID, &tr.ProviderTransferID, &tr.Coin, &tr.TxID, &tr.Direction,
		&amount, &tr.ReceiveAddress, &state, &tr.ProviderState, &tr.Confirmations, &tr.BlockHeight, &tr.OccurredAt, &tr.Version)
	if err != nil {
		return nil, err
	}
	tr.State = domain.State(state)
	tr.Amount, _ = new(big.Int).SetString(amount, 10)
	return &tr, nil
}

func (t *Tx) TransferForUpdate(walletID uuid.UUID, providerTransferID string) (*domain.Transfer, error) {
	tr, err := scanTransfer(t.tx.QueryRow(t.ctx, `SELECT `+transferCols+` FROM app.transfers
		WHERE provider = 'bitgo' AND wallet_id = $1 AND provider_transfer_id = $2 FOR UPDATE`, walletID, providerTransferID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return tr, err
}

func (t *Tx) InsertTransfer(tr *domain.Transfer) (uuid.UUID, error) {
	var id uuid.UUID
	err := t.tx.QueryRow(t.ctx, `
		INSERT INTO app.transfers (organization_id, wallet_id, provider_transfer_id, coin, txid, direction,
		                           amount, receive_address, state, provider_state, confirmations, block_height, occurred_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7::numeric, $8, $9, $10, $11, $12, $13)
		RETURNING id`,
		t.Org, tr.WalletID, tr.ProviderTransferID, tr.Coin, tr.TxID, tr.Direction,
		tr.Amount.String(), tr.ReceiveAddress, string(tr.State), tr.ProviderState, tr.Confirmations, tr.BlockHeight, tr.OccurredAt,
	).Scan(&id)
	return id, err
}

func (t *Tx) UpdateTransfer(tr *domain.Transfer) (bool, error) {
	ct, err := t.tx.Exec(t.ctx, `
		UPDATE app.transfers SET state = $1, provider_state = $2, confirmations = $3, block_height = $4,
		       txid = $5, updated_at = now(), version = version + 1
		WHERE id = $6 AND version = $7`,
		string(tr.State), tr.ProviderState, tr.Confirmations, tr.BlockHeight, tr.TxID, tr.ID, tr.Version)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// ---- audit & alerts ------------------------------------------------------------

func (t *Tx) AppendAudit(org uuid.UUID, actor, action, entityType, entityID string, data map[string]any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO app.audit_logs (organization_id, actor, action, entity_type, entity_id, data)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`, org, actor, action, entityType, entityID, string(b))
	return err
}

func (t *Tx) RaiseAlert(org uuid.UUID, kind, severity, message string, data map[string]any, dedupeKey string) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = t.tx.Exec(t.ctx, `INSERT INTO app.security_alerts (organization_id, kind, severity, message, data, dedupe_key)
		VALUES ($1, $2, $3, $4, $5::jsonb, NULLIF($6, ''))
		ON CONFLICT (organization_id, dedupe_key) WHERE resolved_at IS NULL AND dedupe_key IS NOT NULL DO NOTHING`,
		org, kind, severity, message, string(b), dedupeKey)
	return err
}

// ---- webhook events -------------------------------------------------------------

type WebhookEvent struct {
	ID       uuid.UUID
	Status   string
	Attempts int
}

// RecordWebhookEvent inserts the event or, for a redelivery of the same body,
// bumps its attempt counter. The returned status tells the caller whether it
// was already fully processed.
func (t *Tx) RecordWebhookEvent(key string, body []byte, receivedAt time.Time) (WebhookEvent, error) {
	var ev WebhookEvent
	err := t.tx.QueryRow(t.ctx, `
		INSERT INTO app.webhook_events (organization_id, provider_event_key, body, received_at, attempts)
		VALUES ($1, $2, $3::jsonb, $4, 1)
		ON CONFLICT (organization_id, provider_event_key) DO UPDATE SET attempts = app.webhook_events.attempts + 1
		RETURNING id, status, attempts`, t.Org, key, string(body), receivedAt).Scan(&ev.ID, &ev.Status, &ev.Attempts)
	return ev, err
}

func (t *Tx) MarkWebhookEvent(id uuid.UUID, status, lastError string) error {
	_, err := t.tx.Exec(t.ctx, `UPDATE app.webhook_events SET status = $1, last_error = NULLIF($2, ''),
		processed_at = CASE WHEN $1 IN ('processed', 'ignored', 'rejected') THEN now() ELSE processed_at END
		WHERE id = $3`, status, lastError, id)
	return err
}
