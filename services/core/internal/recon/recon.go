// Package recon reconciles an organization's ledger against BitGo and against
// the customer's expected payments (plan §5.2).
//
// Pass 1 (ledger vs BitGo): every BitGo transfer in the window is applied
// through the domain service, which backfills transfers whose webhook never
// arrived (missed_webhook) and corrects stale states (status_mismatch).
// Pass 2 (expected payments vs receives): matched, amount_mismatch,
// missing_bitgo (overdue) and missing_internal (unexpected inbound).
package recon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/rules"
	"github.com/vaultops/core/internal/store"
)

const actor = "system:recon"

type BitGo interface {
	ListWallets(ctx context.Context) ([]bitgo.Wallet, error)
	ListTransfers(ctx context.Context, coin, walletID, prevID string, limit int) (*bitgo.TransferPage, error)
}

type Engine struct {
	Store    *store.Store
	BitGoFor func(ctx context.Context, org uuid.UUID) (BitGo, error)
	Log      *slog.Logger
	Now      func() time.Time
	Lookback time.Duration // how far back each run re-checks BitGo (default 7 days)
}

type Summary struct {
	RunID uuid.UUID      `json:"runId"`
	Stats map[string]int `json:"stats"`
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Run reconciles one organization and records the run and its findings.
func (e *Engine) Run(ctx context.Context, org uuid.UUID, trigger string) (Summary, error) {
	lookback := e.Lookback
	if lookback == 0 {
		lookback = 7 * 24 * time.Hour
	}
	windowStart := e.now().Add(-lookback)
	var run uuid.UUID
	if err := e.Store.InOrg(ctx, org, func(tx *store.Tx) error {
		var err error
		run, err = tx.CreateRun(trigger, windowStart)
		return err
	}); err != nil {
		return Summary{}, err
	}
	stats := map[string]int{}
	err := e.run(ctx, org, run, windowStart, stats)
	status, msg := "succeeded", ""
	if err != nil {
		status, msg = "failed", sanitize(err)
	}
	if ferr := e.Store.InOrg(ctx, org, func(tx *store.Tx) error {
		if err != nil {
			if aerr := tx.RaiseAlert(org, "reconciliation_failed", "medium", "reconciliation run failed: "+msg,
				map[string]any{"run_id": run.String()}, "recon_failed"); aerr != nil {
				return aerr
			}
		}
		return tx.FinishRun(run, status, stats, msg)
	}); ferr != nil && err == nil {
		err = ferr
	}
	return Summary{RunID: run, Stats: stats}, err
}

func (e *Engine) run(ctx context.Context, org, run uuid.UUID, windowStart time.Time, stats map[string]int) error {
	bg, err := e.BitGoFor(ctx, org)
	if err != nil {
		return err
	}
	wallets, err := bg.ListWallets(ctx)
	if err != nil {
		return err
	}
	for _, w := range wallets {
		transfers, err := windowTransfers(ctx, bg, w, windowStart)
		if err != nil {
			return err
		}
		if err := e.Store.InOrg(ctx, org, func(tx *store.Tx) error {
			return ledgerPass(tx, org, run, w, transfers, stats)
		}); err != nil {
			return err
		}
		stats["transfers_checked"] += len(transfers)
	}
	return e.Store.InOrg(ctx, org, func(tx *store.Tx) error {
		return matchPass(tx, org, run, windowStart, e.now(), stats)
	})
}

// windowTransfers pages newest-first until it passes the window start.
func windowTransfers(ctx context.Context, bg BitGo, w bitgo.Wallet, since time.Time) ([]bitgo.Transfer, error) {
	var out []bitgo.Transfer
	prev := ""
	for page := 0; page < 200; page++ {
		p, err := bg.ListTransfers(ctx, w.Coin, w.ID, prev, 50)
		if err != nil {
			return nil, err
		}
		for _, t := range p.Transfers {
			if !t.Date.IsZero() && t.Date.Before(since) {
				return out, nil
			}
			out = append(out, t)
		}
		if p.NextBatchPrevID == "" || len(p.Transfers) == 0 {
			return out, nil
		}
		prev = p.NextBatchPrevID
	}
	return out, nil
}

func ledgerPass(tx *store.Tx, org, run uuid.UUID, w bitgo.Wallet, transfers []bitgo.Transfer, stats map[string]int) error {
	walletID, err := tx.UpsertWallet(w)
	if err != nil {
		return err
	}
	if err := rules.OnWalletSynced(tx, org, walletID, w); err != nil {
		return err
	}
	// Oldest first, so state history is applied in order.
	sort.SliceStable(transfers, func(i, j int) bool { return transfers[i].Date.Before(transfers[j].Date) })
	for i := range transfers {
		t := &transfers[i]
		obs, err := domain.FromProvider(org, walletID, t)
		if err != nil {
			stats["skipped_unparseable"]++
			continue
		}
		before, err := tx.TransferForUpdate(walletID, t.ID)
		if err != nil {
			return err
		}
		outcome, err := domain.ApplyObservation(tx, actor, obs)
		if err != nil {
			return err
		}
		if err := rules.OnTransfer(tx, org, obs, outcome); err != nil {
			return err
		}
		switch {
		case before == nil:
			stats["missed_webhook"]++
			if err := tx.AddReconItem(run, "missed_webhook", &obs.ID, nil, map[string]any{
				"provider_transfer_id": t.ID, "state": obs.State, "amount": obs.Amount.String(), "coin": t.Coin,
			}); err != nil {
				return err
			}
			if err := tx.RaiseAlert(org, "webhook_gap", "low",
				fmt.Sprintf("transfer %s was missing from the ledger (webhook not received); backfilled", t.ID),
				map[string]any{"provider_transfer_id": t.ID}, "webhook_gap:"+t.ID); err != nil {
				return err
			}
		case outcome == domain.OutcomeRejected || before.State != obs.State:
			stats["status_mismatch"]++
			if err := tx.AddReconItem(run, "status_mismatch", &before.ID, nil, map[string]any{
				"provider_transfer_id": t.ID, "ledger_state": before.State, "bitgo_state": obs.State, "outcome": outcome,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func matchPass(tx *store.Tx, org, run uuid.UUID, windowStart, now time.Time, stats map[string]int) error {
	enabled, err := tx.HasExpectedPayments()
	if err != nil || !enabled {
		return err // without expected payments there is nothing to match against
	}
	open, err := tx.OpenExpectedPayments()
	if err != nil {
		return err
	}
	receives, err := tx.UnmatchedReceives(time.Time{})
	if err != nil {
		return err
	}
	used := map[uuid.UUID]bool{}
	key := func(coin, addr string) string { return coin + "|" + domain.NormalizeAddress(addr) }
	byAddr := map[string][]store.Receive{}
	for _, r := range receives {
		byAddr[key(r.Coin, r.Address)] = append(byAddr[key(r.Coin, r.Address)], r)
	}

	for _, ep := range open {
		want, ok := new(big.Int).SetString(ep.Amount, 10)
		if !ok {
			continue
		}
		var exact, other *store.Receive
		for i, r := range byAddr[key(ep.Coin, ep.Address)] {
			if used[r.ID] {
				continue
			}
			c := byAddr[key(ep.Coin, ep.Address)][i]
			if r.Amount.Cmp(want) == 0 && exact == nil {
				exact = &c
			} else if other == nil {
				other = &c
			}
		}
		epID := ep.ID
		switch {
		case exact != nil:
			used[exact.ID] = true
			stats["matched"]++
			if err := settle(tx, run, ep, exact, "matched", "matched", nil); err != nil {
				return err
			}
		case other != nil:
			used[other.ID] = true
			stats["amount_mismatch"]++
			if err := settle(tx, run, ep, other, "mismatched", "amount_mismatch", map[string]any{
				"expected": ep.Amount, "received": other.Amount.String(),
			}); err != nil {
				return err
			}
			if err := tx.RaiseAlert(org, "recon_amount_mismatch", "medium",
				fmt.Sprintf("payment %q: expected %s, received %s", ep.Reference, ep.Amount, other.Amount),
				map[string]any{"reference": ep.Reference}, "recon_amount:"+ep.ID.String()); err != nil {
				return err
			}
		case ep.ExpectedBy != nil && now.After(*ep.ExpectedBy):
			seen, err := tx.ItemExists("missing_bitgo", nil, &epID)
			if err != nil {
				return err
			}
			if seen {
				continue
			}
			stats["missing_bitgo"]++
			if err := tx.AddReconItem(run, "missing_bitgo", nil, &epID, map[string]any{
				"reference": ep.Reference, "expected_by": ep.ExpectedBy, "amount": ep.Amount, "address": ep.Address,
			}); err != nil {
				return err
			}
			if err := tx.RaiseAlert(org, "recon_payment_overdue", "medium",
				fmt.Sprintf("payment %q is overdue: nothing received at BitGo", ep.Reference),
				map[string]any{"reference": ep.Reference}, "recon_overdue:"+ep.ID.String()); err != nil {
				return err
			}
		}
	}

	for _, r := range receives {
		if used[r.ID] || r.OccurredAt.Before(windowStart) {
			continue
		}
		rid := r.ID
		seen, err := tx.ItemExists("missing_internal", &rid, nil)
		if err != nil {
			return err
		}
		if seen {
			continue
		}
		stats["missing_internal"]++
		if err := tx.AddReconItem(run, "missing_internal", &rid, nil, map[string]any{
			"provider_transfer_id": r.ProviderTransferID, "address": r.Address, "amount": r.Amount.String(), "coin": r.Coin,
		}); err != nil {
			return err
		}
		if err := tx.RaiseAlert(org, "recon_unexpected_inbound", "low",
			fmt.Sprintf("unexpected inbound %s %s to %s", r.Amount, r.Coin, r.Address),
			map[string]any{"provider_transfer_id": r.ProviderTransferID}, "recon_unexpected:"+r.ID.String()); err != nil {
			return err
		}
	}
	return nil
}

func settle(tx *store.Tx, run uuid.UUID, ep store.ExpectedPayment, r *store.Receive, status, kind string, details map[string]any) error {
	if err := tx.SettleExpectedPayment(ep.ID, status, r.ID); err != nil {
		return err
	}
	if details == nil {
		details = map[string]any{}
	}
	details["reference"], details["provider_transfer_id"] = ep.Reference, r.ProviderTransferID
	epID, rid := ep.ID, r.ID
	if err := tx.AddReconItem(run, kind, &rid, &epID, details); err != nil {
		return err
	}
	return tx.AppendAudit(tx.Org, actor, "expected_payment."+status, "expected_payment", ep.ID.String(), details)
}

func sanitize(err error) string {
	var be *bitgo.Error
	if errors.As(err, &be) {
		return fmt.Sprintf("bitgo status %d", be.Status)
	}
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
