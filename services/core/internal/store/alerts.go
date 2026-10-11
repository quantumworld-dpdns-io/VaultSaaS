package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AlertSettings struct {
	LargeTransfer map[string]string `json:"largeTransfer"` // coin -> base units threshold
	DLQThreshold  int               `json:"dlqThreshold"`
	TokenWarnDays int               `json:"tokenWarnDays"`
}

func DefaultAlertSettings() AlertSettings {
	return AlertSettings{LargeTransfer: map[string]string{}, DLQThreshold: 3, TokenWarnDays: 14}
}

func (t *Tx) AlertSettings() (AlertSettings, error) {
	s := DefaultAlertSettings()
	var raw string
	err := t.tx.QueryRow(t.ctx, `SELECT large_transfer::text, dlq_threshold, token_warn_days FROM app.alert_settings`).
		Scan(&raw, &s.DLQThreshold, &s.TokenWarnDays)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal([]byte(raw), &s.LargeTransfer)
}

func (t *Tx) PutAlertSettings(s AlertSettings) error {
	b, _ := json.Marshal(s.LargeTransfer)
	_, err := t.tx.Exec(t.ctx, `INSERT INTO app.alert_settings (organization_id, large_transfer, dlq_threshold, token_warn_days)
		VALUES ($1, $2::jsonb, $3, $4)
		ON CONFLICT (organization_id) DO UPDATE SET large_transfer = EXCLUDED.large_transfer,
		  dlq_threshold = EXCLUDED.dlq_threshold, token_warn_days = EXCLUDED.token_warn_days, updated_at = now()`,
		t.Org, string(b), s.DLQThreshold, s.TokenWarnDays)
	return err
}

// RememberDestination records an outgoing address; true if never seen before.
func (t *Tx) RememberDestination(coin, address string, transfer uuid.UUID) (bool, error) {
	ct, err := t.tx.Exec(t.ctx, `INSERT INTO app.known_destinations (organization_id, coin, address, first_transfer)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, t.Org, coin, address, transfer)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

// RecordWalletPolicy stores the policy hash; true if it differs from a
// previously recorded one (first sighting is not a change).
func (t *Tx) RecordWalletPolicy(wallet uuid.UUID, hash []byte) (bool, error) {
	var old []byte
	err := t.tx.QueryRow(t.ctx, `SELECT policy_hash FROM app.wallets WHERE id = $1 FOR UPDATE`, wallet).Scan(&old)
	if err != nil {
		return false, err
	}
	if _, err := t.tx.Exec(t.ctx, `UPDATE app.wallets SET policy_hash = $2 WHERE id = $1`, wallet, hash); err != nil {
		return false, err
	}
	return old != nil && !bytes.Equal(old, hash), nil
}

func (t *Tx) FailedWebhookEventsSince(since time.Time) (int, error) {
	var n int
	err := t.tx.QueryRow(t.ctx, `SELECT count(*) FROM app.webhook_events WHERE status = 'failed' AND received_at >= $1`, since).Scan(&n)
	return n, err
}

// ResolveAlert marks an open alert resolved; false if not found or already resolved.
func (t *Tx) ResolveAlert(id uuid.UUID) (bool, error) {
	ct, err := t.tx.Exec(t.ctx, `UPDATE app.security_alerts SET resolved_at = now() WHERE id = $1 AND resolved_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}
