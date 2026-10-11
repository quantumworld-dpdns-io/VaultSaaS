// Package rules raises security alerts (plan §6 M5):
//
//	large_transfer       |amount| >= the org's per-coin threshold
//	new_destination      a send to an address this org has never paid before
//	wallet_policy_changed the BitGo wallet policy differs from the last one seen
//	dlq_backlog          failed webhook deliveries in 24h >= threshold
//	bitgo_token_expiring / bitgo_token_expired
//
// Rules run inside the caller's org transaction, so an alert is committed
// together with the ledger change that triggered it.
package rules

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/store"
)

// Tx is the storage the rules need (implemented by *store.Tx).
type Tx interface {
	AlertSettings() (store.AlertSettings, error)
	RaiseAlert(org uuid.UUID, kind, severity, message string, data map[string]any, dedupeKey string) error
	RememberDestination(coin, address string, transfer uuid.UUID) (bool, error)
	RecordWalletPolicy(wallet uuid.UUID, hash []byte) (bool, error)
	FailedWebhookEventsSince(since time.Time) (int, error)
	BitGoConnection() (*store.BitGoConnection, error)
}

var _ Tx = (*store.Tx)(nil)

// OnTransfer evaluates transfer rules after domain.ApplyObservation.
func OnTransfer(tx Tx, org uuid.UUID, t *domain.Transfer, outcome domain.Outcome) error {
	if outcome != domain.OutcomeBooked && outcome != domain.OutcomeUpdated {
		return nil
	}
	if t.State == domain.StateFailed {
		return nil
	}
	settings, err := tx.AlertSettings()
	if err != nil {
		return err
	}
	if limit, ok := settings.LargeTransfer[t.Coin]; ok {
		threshold, ok := new(big.Int).SetString(limit, 10)
		if ok && threshold.Sign() > 0 && new(big.Int).Abs(t.Amount).Cmp(threshold) >= 0 {
			if err := tx.RaiseAlert(org, "large_transfer", "high",
				fmt.Sprintf("%s %s of %s base units (threshold %s)", t.Coin, t.Direction, new(big.Int).Abs(t.Amount), limit),
				map[string]any{"provider_transfer_id": t.ProviderTransferID, "amount": t.Amount.String(), "threshold": limit},
				"large:"+t.ID.String()); err != nil {
				return err
			}
		}
	}
	if t.Direction == "send" && t.DestinationAddress != "" {
		isNew, err := tx.RememberDestination(t.Coin, t.DestinationAddress, t.ID)
		if err != nil {
			return err
		}
		if isNew {
			if err := tx.RaiseAlert(org, "new_destination", "medium",
				fmt.Sprintf("first %s payment to %s", t.Coin, t.DestinationAddress),
				map[string]any{"provider_transfer_id": t.ProviderTransferID, "address": t.DestinationAddress, "amount": t.Amount.String()},
				"newdest:"+t.Coin+":"+t.DestinationAddress); err != nil {
				return err
			}
		}
	}
	return nil
}

// PolicyHash fingerprints a wallet's BitGo policy (nil when BitGo sent none).
func PolicyHash(w bitgo.Wallet) []byte {
	if len(w.Admin) == 0 {
		return nil
	}
	var v struct {
		Policy json.RawMessage `json:"policy"`
	}
	if json.Unmarshal(w.Admin, &v) != nil || len(v.Policy) == 0 {
		return nil
	}
	// Re-marshal through a generic value so key order and spacing don't matter.
	var generic any
	if json.Unmarshal(v.Policy, &generic) != nil {
		return nil
	}
	canon, _ := json.Marshal(generic)
	sum := sha256.Sum256(canon)
	return sum[:]
}

// OnWalletSynced compares the wallet's policy with the last one recorded.
func OnWalletSynced(tx Tx, org, walletID uuid.UUID, w bitgo.Wallet) error {
	h := PolicyHash(w)
	if h == nil {
		return nil
	}
	changed, err := tx.RecordWalletPolicy(walletID, h)
	if err != nil || !changed {
		return err
	}
	return tx.RaiseAlert(org, "wallet_policy_changed", "high",
		fmt.Sprintf("the BitGo policy of wallet %s (%s) changed", w.Label, w.ID),
		map[string]any{"wallet": w.ID, "policy_hash": fmt.Sprintf("%x", h)},
		fmt.Sprintf("policy:%s:%x", w.ID, h))
}

// Scheduled evaluates time-based rules for one org.
func Scheduled(tx Tx, org uuid.UUID, now time.Time) error {
	settings, err := tx.AlertSettings()
	if err != nil {
		return err
	}
	failed, err := tx.FailedWebhookEventsSince(now.Add(-24 * time.Hour))
	if err != nil {
		return err
	}
	if failed >= settings.DLQThreshold {
		if err := tx.RaiseAlert(org, "dlq_backlog", "high",
			fmt.Sprintf("%d webhook deliveries failed in the last 24h (threshold %d)", failed, settings.DLQThreshold),
			map[string]any{"failed_24h": failed}, "dlq_backlog:"+now.Format("2006-01-02")); err != nil {
			return err
		}
	}
	conn, err := tx.BitGoConnection()
	if err != nil || conn == nil || conn.TokenExpiresAt == nil {
		return err
	}
	exp := *conn.TokenExpiresAt
	switch {
	case !now.Before(exp):
		return tx.RaiseAlert(org, "bitgo_token_expired", "critical",
			"the BitGo access token has expired; ingestion and reconciliation will fail until it is replaced",
			map[string]any{"expired_at": exp}, "token_expired:"+exp.Format(time.RFC3339))
	case exp.Sub(now) <= time.Duration(settings.TokenWarnDays)*24*time.Hour:
		days := int(exp.Sub(now).Hours() / 24)
		return tx.RaiseAlert(org, "bitgo_token_expiring", "medium",
			fmt.Sprintf("the BitGo access token expires in %d day(s)", days),
			map[string]any{"expires_at": exp}, "token_expiring:"+exp.Format(time.RFC3339))
	}
	return nil
}
