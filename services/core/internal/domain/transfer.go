// Package domain holds ledger rules. It is the only code that decides ledger
// state transitions (core principle 1); storage and transport call into it.
package domain

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
)

type State string

const (
	StatePending   State = "pending"
	StateConfirmed State = "confirmed"
	StateFailed    State = "failed"
)

// MapProviderState maps a BitGo transfer state onto the ledger state.
func MapProviderState(s string) (State, error) {
	switch strings.ToLower(s) {
	case "unconfirmed", "signed", "pendingapproval", "initialized":
		return StatePending, nil
	case "confirmed":
		return StateConfirmed, nil
	case "failed", "rejected", "removed":
		return StateFailed, nil
	}
	return "", fmt.Errorf("domain: unknown provider state %q", s)
}

// CanTransition encodes the ledger state machine.
//
//	pending   -> confirmed | failed
//	confirmed -> failed            (chain reorg / removal; raises an alert)
//	confirmed -> pending           (reorg back to unconfirmed; raises an alert)
//	failed    -> (terminal)
func CanTransition(from, to State) bool {
	if from == to {
		return true
	}
	switch from {
	case StatePending:
		return to == StateConfirmed || to == StateFailed
	case StateConfirmed:
		return to == StateFailed || to == StatePending
	}
	return false
}

// Transfer is the ledger view of a provider transfer.
type Transfer struct {
	ID                 uuid.UUID
	OrganizationID     uuid.UUID
	WalletID           uuid.UUID
	ProviderTransferID string
	Coin               string
	TxID               string
	Direction          string
	Amount             *big.Int // signed base units
	State              State
	ProviderState      string
	Confirmations      int64
	BlockHeight        *int64
	OccurredAt         time.Time
	Version            int
}

// FromProvider builds the ledger representation of an authoritative BitGo
// transfer (fetched from the API, never taken from a webhook body).
func FromProvider(org, wallet uuid.UUID, t *bitgo.Transfer) (*Transfer, error) {
	st, err := MapProviderState(t.State)
	if err != nil {
		return nil, err
	}
	amt, ok := new(big.Int).SetString(t.ValueString, 10)
	if !ok {
		return nil, fmt.Errorf("domain: invalid amount %q", t.ValueString)
	}
	dir := strings.ToLower(t.Type)
	if dir != "send" && dir != "receive" {
		return nil, fmt.Errorf("domain: unknown transfer type %q", t.Type)
	}
	var height *int64
	if t.Height > 0 {
		h := t.Height
		height = &h
	}
	occurred := t.Date
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	return &Transfer{
		OrganizationID: org, WalletID: wallet, ProviderTransferID: t.ID, Coin: t.Coin, TxID: t.TxID,
		Direction: dir, Amount: amt, State: st, ProviderState: t.State, Confirmations: t.Confirmations,
		BlockHeight: height, OccurredAt: occurred,
	}, nil
}

// Ledger is the storage the domain needs, implemented by store.Tx.
type Ledger interface {
	TransferForUpdate(walletID uuid.UUID, providerTransferID string) (*Transfer, error) // nil if absent
	InsertTransfer(t *Transfer) (uuid.UUID, error)
	// UpdateTransfer must apply only if the stored version equals t.Version
	// (optimistic lock) and returns false otherwise.
	UpdateTransfer(t *Transfer) (bool, error)
	AppendAudit(org uuid.UUID, actor, action, entityType, entityID string, data map[string]any) error
	RaiseAlert(org uuid.UUID, kind, severity, message string, data map[string]any, dedupeKey string) error
}

var ErrConcurrentUpdate = errors.New("domain: transfer changed concurrently")

// Outcome describes what ApplyObservation did.
type Outcome string

const (
	OutcomeBooked    Outcome = "booked"
	OutcomeUpdated   Outcome = "updated"
	OutcomeUnchanged Outcome = "unchanged"
	// OutcomeRejected: the observation contradicts the ledger. Nothing was
	// changed; an alert was raised and must be committed by the caller.
	OutcomeRejected Outcome = "rejected"
)

// ApplyObservation books or advances a transfer from an authoritative
// observation. It is idempotent: the same observation twice is a no-op.
func ApplyObservation(l Ledger, actor string, obs *Transfer) (Outcome, error) {
	cur, err := l.TransferForUpdate(obs.WalletID, obs.ProviderTransferID)
	if err != nil {
		return "", err
	}
	if cur == nil {
		id, err := l.InsertTransfer(obs)
		if err != nil {
			return "", err
		}
		obs.ID = id
		return OutcomeBooked, l.AppendAudit(obs.OrganizationID, actor, "transfer.booked", "transfer", id.String(), map[string]any{
			"provider_transfer_id": obs.ProviderTransferID, "state": obs.State, "amount": obs.Amount.String(), "coin": obs.Coin,
		})
	}

	if cur.Amount.Cmp(obs.Amount) != 0 || cur.Direction != obs.Direction || cur.Coin != obs.Coin {
		// The provider changed an immutable fact; never silently rewrite money.
		return rejected(l.RaiseAlert(obs.OrganizationID, "transfer_mutated", "high",
			"BitGo reported different amount/direction/coin for an existing transfer",
			map[string]any{"transfer_id": cur.ID.String(), "stored_amount": cur.Amount.String(), "observed_amount": obs.Amount.String()},
			"transfer_mutated:"+cur.ID.String()))
	}
	if cur.State == obs.State && cur.ProviderState == obs.ProviderState && cur.Confirmations == obs.Confirmations {
		return OutcomeUnchanged, nil
	}
	if !CanTransition(cur.State, obs.State) {
		return rejected(l.RaiseAlert(obs.OrganizationID, "illegal_transition", "medium",
			fmt.Sprintf("transfer %s: %s -> %s rejected", cur.ID, cur.State, obs.State),
			map[string]any{"transfer_id": cur.ID.String(), "from": cur.State, "to": obs.State},
			"illegal_transition:"+cur.ID.String()))
	}

	next := *cur
	next.State, next.ProviderState, next.Confirmations = obs.State, obs.ProviderState, obs.Confirmations
	next.BlockHeight, next.TxID = obs.BlockHeight, obs.TxID
	ok, err := l.UpdateTransfer(&next)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrConcurrentUpdate
	}
	if cur.State != obs.State {
		if err := l.AppendAudit(obs.OrganizationID, actor, "transfer.state_changed", "transfer", cur.ID.String(), map[string]any{
			"from": cur.State, "to": obs.State, "provider_state": obs.ProviderState,
		}); err != nil {
			return "", err
		}
		if cur.State == StateConfirmed {
			if err := l.RaiseAlert(obs.OrganizationID, "confirmed_transfer_reverted", "high",
				fmt.Sprintf("confirmed transfer %s moved to %s (provider state %s)", cur.ID, obs.State, obs.ProviderState),
				map[string]any{"transfer_id": cur.ID.String(), "to": obs.State}, "reverted:"+cur.ID.String()); err != nil {
				return "", err
			}
		}
	}
	return OutcomeUpdated, nil
}

func rejected(alertErr error) (Outcome, error) {
	if alertErr != nil {
		return "", alertErr
	}
	return OutcomeRejected, nil
}
