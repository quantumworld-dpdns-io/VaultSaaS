package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

const maxUint256 = "115792089237316195423570985008687907853269984665640564039457584007913129639935"

func TestLedgerRoundTripAndIdempotency(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	org, token, err := s.CreateOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.OrganizationByWebhookToken(ctx, token)
	if err != nil || got.ID != org.ID {
		t.Fatalf("lookup by token: %v %v", got, err)
	}
	if _, err := s.OrganizationByWebhookToken(ctx, "nope"); err != store.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	for i := 0; i < 3; i++ {
		err := s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
			wid, err := tx.UpsertWallet(bitgo.Wallet{ID: "w1", Coin: "hteth", Label: "Treasury", BalanceString: maxUint256})
			if err != nil {
				return err
			}
			obs, err := domain.FromProvider(org.ID, wid, &bitgo.Transfer{ID: "t1", Coin: "hteth", Type: "receive",
				ValueString: maxUint256, State: "confirmed", Date: time.Now()})
			if err != nil {
				return err
			}
			_, err = domain.ApplyObservation(tx, "test", obs)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	oc, err := pgx.Connect(ctx, owner)
	if err != nil {
		t.Fatal(err)
	}
	defer oc.Close(ctx)
	var n, audits int
	var amount string
	if err := oc.QueryRow(ctx, `SELECT count(*), max(amount::text) FROM app.transfers`).Scan(&n, &amount); err != nil {
		t.Fatal(err)
	}
	oc.QueryRow(ctx, `SELECT count(*) FROM app.audit_logs WHERE action = 'transfer.booked'`).Scan(&audits)
	if n != 1 || audits != 1 || amount != maxUint256 {
		t.Fatalf("transfers=%d audits=%d amount=%s", n, audits, amount)
	}
}

func TestWebhookEventDedupeAndAlertDedupe(t *testing.T) {
	s, _ := storetest.New(t)
	ctx := context.Background()
	org, _, _ := s.CreateOrganization(ctx, "Acme")
	var last store.WebhookEvent
	for i := 0; i < 3; i++ {
		if err := s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
			ev, err := tx.RecordWebhookEvent("key-1", []byte(`{"type":"transfer"}`), time.Now())
			last = ev
			if err != nil {
				return err
			}
			return tx.RaiseAlert(org.ID, "k", "low", "m", nil, "same")
		}); err != nil {
			t.Fatal(err)
		}
	}
	if last.Attempts != 3 || last.Status != "received" {
		t.Fatalf("got %+v", last)
	}
	var alerts int
	s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
		return tx.QueryRowForTest(`SELECT count(*) FROM app.security_alerts`).Scan(&alerts)
	})
	if alerts != 1 {
		t.Fatalf("open alert with same dedupe key must be raised once, got %d", alerts)
	}
}
