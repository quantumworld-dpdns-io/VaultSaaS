package rules_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/rules"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

type env struct {
	t     *testing.T
	s     *store.Store
	owner string
	org   uuid.UUID
}

func setup(t *testing.T) *env {
	s, owner := storetest.New(t)
	u := storetest.CreateUser(t, owner, "ops@example.com")
	org, _, err := s.CreateOrganizationWithOwner(context.Background(), "Acme", u)
	if err != nil {
		t.Fatal(err)
	}
	return &env{t, s, owner, org.ID}
}

func (e *env) tx(fn func(*store.Tx) error) {
	e.t.Helper()
	if err := e.s.InOrg(context.Background(), e.org, fn); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) alerts(kind string) int {
	var n int
	storetest.Owner(e.t, e.owner).QueryRow(context.Background(),
		`SELECT count(*) FROM app.security_alerts WHERE kind = $1`, kind).Scan(&n)
	return n
}

func (e *env) book(tr bitgo.Transfer) {
	e.t.Helper()
	e.tx(func(tx *store.Tx) error {
		wid, err := tx.UpsertWallet(bitgo.Wallet{ID: tr.Wallet, Coin: tr.Coin})
		if err != nil {
			return err
		}
		obs, err := domain.FromProvider(e.org, wid, &tr)
		if err != nil {
			return err
		}
		out, err := domain.ApplyObservation(tx, "test", obs)
		if err != nil {
			return err
		}
		return rules.OnTransfer(tx, e.org, obs, out)
	})
}

func send(id, to, value string) bitgo.Transfer {
	return bitgo.Transfer{ID: id, Coin: "tbtc4", Wallet: "w1", Type: "send", ValueString: "-" + value, State: "confirmed", Date: time.Now(),
		Entries: []bitgo.TransferEntry{{Address: "change", ValueString: "-" + value, Wallet: "w1"}, {Address: to, ValueString: value}}}
}

func TestLargeTransferAndNewDestination(t *testing.T) {
	e := setup(t)
	e.tx(func(tx *store.Tx) error {
		return tx.PutAlertSettings(store.AlertSettings{LargeTransfer: map[string]string{"tbtc4": "100000000"}, DLQThreshold: 3, TokenWarnDays: 14})
	})
	e.book(send("s1", "bc1qdest", "5000"))       // new destination, small
	e.book(send("s2", "bc1qdest", "6000"))       // known destination, small
	e.book(send("s3", "bc1qother", "150000000")) // new destination, large
	if n := e.alerts("new_destination"); n != 2 {
		t.Errorf("new_destination alerts: %d", n)
	}
	if n := e.alerts("large_transfer"); n != 1 {
		t.Errorf("large_transfer alerts: %d", n)
	}
	// Re-applying the same observation does not raise again.
	e.book(send("s3", "bc1qother", "150000000"))
	if n := e.alerts("large_transfer"); n != 1 {
		t.Errorf("large_transfer must be de-duplicated, got %d", n)
	}
}

func TestWalletPolicyChange(t *testing.T) {
	e := setup(t)
	w := bitgo.Wallet{ID: "w1", Coin: "tbtc4", Label: "Ops", Admin: json.RawMessage(`{"policy":{"rules":[{"id":"a","limit":1}]}}`)}
	sync := func(w bitgo.Wallet) {
		e.tx(func(tx *store.Tx) error {
			id, err := tx.UpsertWallet(w)
			if err != nil {
				return err
			}
			return rules.OnWalletSynced(tx, e.org, id, w)
		})
	}
	sync(w) // first sighting is not a change
	w.Admin = json.RawMessage(`{"policy": {"rules": [{"limit": 1, "id": "a"}]}}`)
	sync(w) // same policy, different formatting
	if n := e.alerts("wallet_policy_changed"); n != 0 {
		t.Fatalf("no change expected, got %d", n)
	}
	w.Admin = json.RawMessage(`{"policy":{"rules":[]}}`) // limit removed
	sync(w)
	if n := e.alerts("wallet_policy_changed"); n != 1 {
		t.Fatalf("policy change must alert once, got %d", n)
	}
}

func TestScheduledRules(t *testing.T) {
	e := setup(t)
	now := time.Now()
	e.tx(func(tx *store.Tx) error {
		for i := 0; i < 3; i++ {
			ev, err := tx.RecordWebhookEvent(uuid.NewString(), []byte(`{}`), now)
			if err != nil {
				return err
			}
			if err := tx.MarkWebhookEvent(ev.ID, "failed", "bitgo status 503"); err != nil {
				return err
			}
		}
		exp := now.Add(5 * 24 * time.Hour)
		return tx.PutBitGoConnection(store.BitGoConnection{Environment: "test", EncryptedToken: []byte{1}, KeyVersion: 1,
			OwnerUserID: storetest.CreateUser(t, e.owner, "a@example.com"), VerifiedAt: now, TokenExpiresAt: &exp})
	})
	e.tx(func(tx *store.Tx) error { return rules.Scheduled(tx, e.org, now) })
	if e.alerts("dlq_backlog") != 1 || e.alerts("bitgo_token_expiring") != 1 {
		t.Fatalf("dlq=%d expiring=%d", e.alerts("dlq_backlog"), e.alerts("bitgo_token_expiring"))
	}
	e.tx(func(tx *store.Tx) error { return rules.Scheduled(tx, e.org, now.Add(6*24*time.Hour)) })
	if e.alerts("bitgo_token_expired") != 1 {
		t.Fatal("expired token must raise a critical alert")
	}
}
