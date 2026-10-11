package store

import (
	"context"
	"encoding/json"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/vaultops/core/internal/domain"
)

// ---- tenants for scheduled jobs ------------------------------------------

func (s *Store) OrganizationsWithBitGo(ctx context.Context) ([]uuid.UUID, error) {
	return s.uuids(ctx, `SELECT app.organizations_with_bitgo()`)
}

func (s *Store) AllOrganizations(ctx context.Context) ([]uuid.UUID, error) {
	return s.uuids(ctx, `SELECT app.all_organizations()`)
}

func (s *Store) uuids(ctx context.Context, q string) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
}

// ---- expected payments ----------------------------------------------------

type ExpectedPayment struct {
	ID                uuid.UUID  `json:"id"`
	Reference         string     `json:"reference"`
	Coin              string     `json:"coin"`
	Address           string     `json:"address"`
	Amount            string     `json:"amount"`
	ExpectedBy        *time.Time `json:"expectedBy"`
	Status            string     `json:"status"`
	MatchedTransferID *uuid.UUID `json:"matchedTransferId"`
}

// UpsertExpectedPayment inserts or, while still open, updates by reference.
// Returns false when the reference exists and is already settled.
func (t *Tx) UpsertExpectedPayment(p ExpectedPayment) (bool, error) {
	ct, err := t.tx.Exec(t.ctx, `
		INSERT INTO app.expected_payments (organization_id, reference, coin, address, amount, expected_by, created_by)
		VALUES ($1, $2, $3, $4, $5::numeric, $6, $7)
		ON CONFLICT (organization_id, reference) DO UPDATE SET coin = EXCLUDED.coin, address = EXCLUDED.address,
		  amount = EXCLUDED.amount, expected_by = EXCLUDED.expected_by, updated_at = now()
		WHERE app.expected_payments.status = 'open'`,
		t.Org, p.Reference, p.Coin, p.Address, p.Amount, p.ExpectedBy, nullUUID(t.User))
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

func nullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

const epCols = `id, reference, coin, address, amount::text, expected_by, status, matched_transfer_id`

func scanEP(r pgx.CollectableRow) (ExpectedPayment, error) {
	var p ExpectedPayment
	return p, r.Scan(&p.ID, &p.Reference, &p.Coin, &p.Address, &p.Amount, &p.ExpectedBy, &p.Status, &p.MatchedTransferID)
}

func (t *Tx) ListExpectedPayments(limit int) ([]ExpectedPayment, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT `+epCols+` FROM app.expected_payments ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanEP)
}

func (t *Tx) OpenExpectedPayments() ([]ExpectedPayment, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT `+epCols+` FROM app.expected_payments WHERE status = 'open'
		ORDER BY expected_by NULLS LAST, created_at FOR UPDATE`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanEP)
}

func (t *Tx) HasExpectedPayments() (bool, error) {
	var ok bool
	err := t.tx.QueryRow(t.ctx, `SELECT EXISTS (SELECT 1 FROM app.expected_payments)`).Scan(&ok)
	return ok, err
}

func (t *Tx) SettleExpectedPayment(id uuid.UUID, status string, transfer uuid.UUID) error {
	_, err := t.tx.Exec(t.ctx, `UPDATE app.expected_payments SET status = $1, matched_transfer_id = $2, updated_at = now()
		WHERE id = $3`, status, transfer, id)
	return err
}

// Receive is a confirmed inbound transfer available for matching.
type Receive struct {
	ID                 uuid.UUID
	ProviderTransferID string
	Coin               string
	Address            string
	Amount             *big.Int
	OccurredAt         time.Time
}

func scanReceive(r pgx.CollectableRow) (Receive, error) {
	var x Receive
	var amt string
	err := r.Scan(&x.ID, &x.ProviderTransferID, &x.Coin, &x.Address, &amt, &x.OccurredAt)
	x.Amount, _ = new(big.Int).SetString(amt, 10)
	return x, err
}

// UnmatchedReceives returns confirmed receives not yet tied to an expected payment.
func (t *Tx) UnmatchedReceives(since time.Time) ([]Receive, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT t.id, t.provider_transfer_id, t.coin, t.receive_address, t.amount::text, t.occurred_at
		FROM app.transfers t
		WHERE t.direction = 'receive' AND t.state = 'confirmed' AND t.occurred_at >= $1
		  AND NOT EXISTS (SELECT 1 FROM app.expected_payments e WHERE e.matched_transfer_id = t.id)
		ORDER BY t.occurred_at, t.id`, since)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, scanReceive)
}

// ---- runs and items ---------------------------------------------------------

func (t *Tx) CreateRun(trigger string, windowStart time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := t.tx.QueryRow(t.ctx, `INSERT INTO app.reconciliation_runs (organization_id, trigger, window_start)
		VALUES ($1, $2, $3) RETURNING id`, t.Org, trigger, windowStart).Scan(&id)
	return id, err
}

func (t *Tx) FinishRun(id uuid.UUID, status string, stats map[string]int, errMsg string) error {
	b, _ := json.Marshal(stats)
	_, err := t.tx.Exec(t.ctx, `UPDATE app.reconciliation_runs SET status = $1, stats = $2::jsonb, error = NULLIF($3, ''),
		finished_at = now() WHERE id = $4`, status, string(b), errMsg, id)
	return err
}

func (t *Tx) AddReconItem(run uuid.UUID, kind string, transfer, expected *uuid.UUID, details map[string]any) error {
	b, _ := json.Marshal(details)
	_, err := t.tx.Exec(t.ctx, `INSERT INTO app.reconciliation_items (organization_id, run_id, kind, transfer_id, expected_payment_id, details)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)`, t.Org, run, kind, transfer, expected, string(b))
	return err
}

// LastSuccessfulRunStart lets the next run overlap the previous window.
func (t *Tx) LastSuccessfulRunStart() (*time.Time, error) {
	var ts *time.Time
	err := t.tx.QueryRow(t.ctx, `SELECT max(started_at) FROM app.reconciliation_runs WHERE status = 'succeeded'`).Scan(&ts)
	return ts, err
}

type RunRow struct {
	ID          uuid.UUID       `json:"id"`
	Trigger     string          `json:"trigger"`
	Status      string          `json:"status"`
	WindowStart time.Time       `json:"windowStart"`
	StartedAt   time.Time       `json:"startedAt"`
	FinishedAt  *time.Time      `json:"finishedAt"`
	Stats       json.RawMessage `json:"stats"`
	Error       *string         `json:"error"`
}

func (t *Tx) ListRuns(limit int) ([]RunRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT id, trigger, status, window_start, started_at, finished_at, stats::text, error
		FROM app.reconciliation_runs ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (RunRow, error) {
		var x RunRow
		var stats string
		err := r.Scan(&x.ID, &x.Trigger, &x.Status, &x.WindowStart, &x.StartedAt, &x.FinishedAt, &stats, &x.Error)
		x.Stats = json.RawMessage(stats)
		return x, err
	})
}

type ItemRow struct {
	ID                 uuid.UUID       `json:"id"`
	Kind               string          `json:"kind"`
	TransferID         *uuid.UUID      `json:"transferId"`
	ProviderTransferID *string         `json:"providerTransferId"`
	ExpectedPaymentID  *uuid.UUID      `json:"expectedPaymentId"`
	Reference          *string         `json:"reference"`
	Details            json.RawMessage `json:"details"`
	CreatedAt          time.Time       `json:"createdAt"`
}

func (t *Tx) ListRunItems(run uuid.UUID) ([]ItemRow, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT i.id, i.kind, i.transfer_id, tr.provider_transfer_id, i.expected_payment_id,
		e.reference, i.details::text, i.created_at
		FROM app.reconciliation_items i
		LEFT JOIN app.transfers tr ON tr.id = i.transfer_id
		LEFT JOIN app.expected_payments e ON e.id = i.expected_payment_id
		WHERE i.run_id = $1 ORDER BY i.kind, i.created_at`, run)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemRow, error) {
		var x ItemRow
		var d string
		err := r.Scan(&x.ID, &x.Kind, &x.TransferID, &x.ProviderTransferID, &x.ExpectedPaymentID, &x.Reference, &d, &x.CreatedAt)
		x.Details = json.RawMessage(d)
		return x, err
	})
}

// TransfersBetween feeds the daily report.
func (t *Tx) TransfersBetween(from, to time.Time) ([]domain.Transfer, error) {
	rows, err := t.tx.Query(t.ctx, `SELECT `+transferCols+` FROM app.transfers
		WHERE occurred_at >= $1 AND occurred_at < $2 ORDER BY occurred_at, id`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Transfer
	for rows.Next() {
		tr, err := scanTransfer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *tr)
	}
	return out, rows.Err()
}
