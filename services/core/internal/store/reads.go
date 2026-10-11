package store

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Read models for the API. Amounts are returned as decimal strings.

type WalletRow struct {
	ID               uuid.UUID  `json:"id"`
	ProviderWalletID string     `json:"providerWalletId"`
	Coin             string     `json:"coin"`
	Label            string     `json:"label"`
	Type             string     `json:"type"`
	Balance          *string    `json:"balance"`
	ConfirmedBalance *string    `json:"confirmedBalance"`
	SpendableBalance *string    `json:"spendableBalance"`
	LastSyncedAt     *time.Time `json:"lastSyncedAt"`
}

func (t *Tx) ListWallets() ([]WalletRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT id, provider_wallet_id, coin, label, wallet_type,
		balance::text, confirmed_balance::text, spendable_balance::text, last_synced_at
		FROM app.wallets ORDER BY label, provider_wallet_id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (WalletRow, error) {
		var w WalletRow
		return w, r.Scan(&w.ID, &w.ProviderWalletID, &w.Coin, &w.Label, &w.Type, &w.Balance, &w.ConfirmedBalance, &w.SpendableBalance, &w.LastSyncedAt)
	})
}

type TransferRow struct {
	ID                 uuid.UUID `json:"id"`
	WalletID           uuid.UUID `json:"walletId"`
	ProviderTransferID string    `json:"providerTransferId"`
	Coin               string    `json:"coin"`
	TxID               string    `json:"txid"`
	Direction          string    `json:"direction"`
	Amount             string    `json:"amount"`
	State              string    `json:"state"`
	Confirmations      int64     `json:"confirmations"`
	OccurredAt         time.Time `json:"occurredAt"`
}

func (t *Tx) ListTransfers(limit int) ([]TransferRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT id, wallet_id, provider_transfer_id, coin, txid, direction, amount::text,
		state, confirmations, occurred_at FROM app.transfers ORDER BY occurred_at DESC, id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (TransferRow, error) {
		var x TransferRow
		return x, r.Scan(&x.ID, &x.WalletID, &x.ProviderTransferID, &x.Coin, &x.TxID, &x.Direction, &x.Amount, &x.State, &x.Confirmations, &x.OccurredAt)
	})
}

type AlertRow struct {
	ID         uuid.UUID       `json:"id"`
	Kind       string          `json:"kind"`
	Severity   string          `json:"severity"`
	Message    string          `json:"message"`
	Data       json.RawMessage `json:"data"`
	CreatedAt  time.Time       `json:"createdAt"`
	ResolvedAt *time.Time      `json:"resolvedAt"`
}

func (t *Tx) ListAlerts(limit int) ([]AlertRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT id, kind, severity, message, data::text, created_at, resolved_at
		FROM app.security_alerts ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AlertRow, error) {
		var a AlertRow
		var data string
		err := r.Scan(&a.ID, &a.Kind, &a.Severity, &a.Message, &data, &a.CreatedAt, &a.ResolvedAt)
		a.Data = json.RawMessage(data)
		return a, err
	})
}

type AuditRow struct {
	ID         int64           `json:"id"`
	OccurredAt time.Time       `json:"occurredAt"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	EntityType string          `json:"entityType"`
	EntityID   string          `json:"entityId"`
	Data       json.RawMessage `json:"data"`
	Hash       string          `json:"hash"`
}

func (t *Tx) ListAudit(limit int) ([]AuditRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT id, occurred_at, actor, action, entity_type, entity_id, data::text, hash
		FROM app.audit_logs ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (AuditRow, error) {
		var a AuditRow
		var data string
		var hash []byte
		err := r.Scan(&a.ID, &a.OccurredAt, &a.Actor, &a.Action, &a.EntityType, &a.EntityID, &data, &hash)
		a.Data, a.Hash = json.RawMessage(data), hex.EncodeToString(hash)
		return a, err
	})
}
