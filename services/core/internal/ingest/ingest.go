// Package ingest turns queued BitGo webhook deliveries into ledger changes.
//
// Core principle 2: a webhook is only a hint that something changed. The body
// is used to find *which* transfer to look at; every booked fact (state,
// amount, confirmations) comes from re-fetching the transfer from BitGo.
package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/connections"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/rules"
	"github.com/vaultops/core/internal/store"
)

// MaxReceives mirrors the SQS redrive policy (maxReceiveCount = 5).
const MaxReceives = 5

const actor = "system:ingest"

// Message is what the edge Worker enqueues for every webhook delivery.
type Message struct {
	V          int       `json:"v"`
	Provider   string    `json:"provider"`
	OrgToken   string    `json:"orgToken"`
	ReceivedAt time.Time `json:"receivedAt"`
	Body       string    `json:"body"` // raw request body, byte-for-byte
}

// event is the subset of BitGo's webhook body used to locate the transfer.
type event struct {
	Type     string `json:"type"`
	Wallet   string `json:"wallet"`
	Transfer string `json:"transfer"`
	Coin     string `json:"coin"`
}

type OrgTx interface {
	domain.Ledger
	rules.Tx
	RecordWebhookEvent(key string, body []byte, receivedAt time.Time) (store.WebhookEvent, error)
	MarkWebhookEvent(id uuid.UUID, status, lastError string) error
	WalletByProviderID(providerWalletID string) (*store.Wallet, error)
	UpsertWallet(w bitgo.Wallet) (uuid.UUID, error)
}

type Store interface {
	OrganizationByWebhookToken(ctx context.Context, token string) (store.Organization, error)
	InOrg(ctx context.Context, org uuid.UUID, fn func(OrgTx) error) error
}

type BitGo interface {
	GetTransfer(ctx context.Context, coin, walletID, transferID string) (*bitgo.Transfer, error)
	GetWallet(ctx context.Context, coin, walletID string) (*bitgo.Wallet, error)
}

// ClientFor returns the BitGo client authenticated as the given org.
type ClientFor func(ctx context.Context, org uuid.UUID) (BitGo, error)

type Processor struct {
	Store    Store
	BitGoFor ClientFor
	Log      *slog.Logger
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_:.-]{1,128}$`)

// Handle processes one delivery. A nil error means "done, delete the
// message"; an error means "retry" (and, after MaxReceives, dead-letter).
func (p *Processor) Handle(ctx context.Context, m Message, receiveCount int) error {
	log := p.Log.With("received_at", m.ReceivedAt, "receive_count", receiveCount)
	if m.V != 1 || m.Provider != "bitgo" {
		log.Warn("dropping message with unknown envelope", "v", m.V, "provider", m.Provider)
		return nil
	}
	org, err := p.Store.OrganizationByWebhookToken(ctx, m.OrgToken)
	if errors.Is(err, store.ErrNotFound) {
		log.Warn("dropping webhook for unknown token")
		return nil
	}
	if err != nil {
		return err
	}
	log = log.With("org", org.ID)

	var ev event
	if err := json.Unmarshal([]byte(m.Body), &ev); err != nil {
		log.Warn("dropping non-JSON webhook body")
		return nil
	}
	sum := sha256.Sum256([]byte(m.Body))
	key := hex.EncodeToString(sum[:])

	var rec store.WebhookEvent
	if err := p.Store.InOrg(ctx, org.ID, func(tx OrgTx) error {
		rec, err = tx.RecordWebhookEvent(key, []byte(m.Body), m.ReceivedAt)
		return err
	}); err != nil {
		return err
	}
	switch rec.Status {
	case "processed", "ignored", "rejected":
		log.Info("duplicate delivery, already handled", "event", rec.ID, "status", rec.Status)
		return nil
	}

	if ev.Type != "transfer" {
		return p.finish(ctx, org.ID, rec.ID, "ignored", "")
	}
	if !idPattern.MatchString(ev.Wallet) || !idPattern.MatchString(ev.Transfer) || !idPattern.MatchString(ev.Coin) {
		return p.reject(ctx, org.ID, rec.ID, key, "webhook body has missing or malformed identifiers", nil)
	}

	bg, err := p.BitGoFor(ctx, org.ID)
	if errors.Is(err, connections.ErrNotConnected) {
		return p.reject(ctx, org.ID, rec.ID, key, "webhook received but the organization has no BitGo connection", nil)
	}
	if err != nil {
		return p.fail(ctx, org.ID, rec.ID, key, receiveCount, err)
	}

	// Principle 2: the authoritative state comes from BitGo, not from the body.
	t, err := bg.GetTransfer(ctx, ev.Coin, ev.Wallet, ev.Transfer)
	if err != nil {
		var be *bitgo.Error
		if errors.As(err, &be) && (be.Status == http.StatusNotFound || be.Status == http.StatusBadRequest) {
			return p.reject(ctx, org.ID, rec.ID, key, "BitGo does not know the transfer named in the webhook",
				map[string]any{"wallet": ev.Wallet, "transfer": ev.Transfer, "coin": ev.Coin})
		}
		return p.fail(ctx, org.ID, rec.ID, key, receiveCount, err)
	}
	if t.ID != ev.Transfer || t.Wallet != ev.Wallet || t.Coin != ev.Coin {
		return p.reject(ctx, org.ID, rec.ID, key, "BitGo transfer does not match the webhook identifiers",
			map[string]any{"wallet": ev.Wallet, "transfer": ev.Transfer})
	}

	var known *store.Wallet
	if err := p.Store.InOrg(ctx, org.ID, func(tx OrgTx) error {
		known, err = tx.WalletByProviderID(t.Wallet)
		return err
	}); err != nil {
		return err
	}
	var fresh *bitgo.Wallet
	if known == nil {
		if fresh, err = bg.GetWallet(ctx, t.Coin, t.Wallet); err != nil {
			return p.fail(ctx, org.ID, rec.ID, key, receiveCount, err)
		}
	}

	var outcome domain.Outcome
	err = p.Store.InOrg(ctx, org.ID, func(tx OrgTx) error {
		walletID := uuid.Nil
		if known != nil {
			walletID = known.ID
		} else {
			if walletID, err = tx.UpsertWallet(*fresh); err != nil {
				return err
			}
			if err := rules.OnWalletSynced(tx, org.ID, walletID, *fresh); err != nil {
				return err
			}
		}
		obs, err := domain.FromProvider(org.ID, walletID, t)
		if err != nil {
			return err
		}
		if outcome, err = domain.ApplyObservation(tx, actor, obs); err != nil {
			return err
		}
		if err := rules.OnTransfer(tx, org.ID, obs, outcome); err != nil {
			return err
		}
		status := "processed"
		if outcome == domain.OutcomeRejected {
			status = "rejected"
		}
		return tx.MarkWebhookEvent(rec.ID, status, "")
	})
	if err != nil {
		return p.fail(ctx, org.ID, rec.ID, key, receiveCount, err)
	}
	log.Info("webhook applied", "event", rec.ID, "transfer", t.ID, "outcome", outcome)
	return nil
}

func (p *Processor) finish(ctx context.Context, org, id uuid.UUID, status, msg string) error {
	return p.Store.InOrg(ctx, org, func(tx OrgTx) error { return tx.MarkWebhookEvent(id, status, msg) })
}

func (p *Processor) reject(ctx context.Context, org, id uuid.UUID, key, msg string, data map[string]any) error {
	return p.Store.InOrg(ctx, org, func(tx OrgTx) error {
		if err := tx.MarkWebhookEvent(id, "rejected", msg); err != nil {
			return err
		}
		return tx.RaiseAlert(org, "webhook_unverifiable", "high", msg, data, "unverifiable:"+key)
	})
}

// fail records the failure and returns an error so SQS retries; on the last
// receive it raises the dead-letter alert before the message moves to the DLQ.
func (p *Processor) fail(ctx context.Context, org, id uuid.UUID, key string, receiveCount int, cause error) error {
	msg := sanitize(cause)
	_ = p.Store.InOrg(ctx, org, func(tx OrgTx) error {
		if err := tx.MarkWebhookEvent(id, "failed", msg); err != nil {
			return err
		}
		if receiveCount >= MaxReceives {
			return tx.RaiseAlert(org, "webhook_dead_lettered", "high",
				fmt.Sprintf("webhook could not be processed after %d attempts; moved to the dead-letter queue", receiveCount),
				map[string]any{"event_id": id.String(), "error": msg}, "dlq:"+key)
		}
		return nil
	})
	return fmt.Errorf("ingest: %s", msg)
}

// sanitize keeps error text useful without persisting upstream bodies.
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
