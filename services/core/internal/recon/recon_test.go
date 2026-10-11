package recon_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/bitgo/bitgomock"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/recon"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

func TestReconClassifiesEveryScenario(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	u := storetest.CreateUser(t, owner, "ops@example.com")
	org, _, err := s.CreateOrganizationWithOwner(ctx, "Acme", u)
	if err != nil {
		t.Fatal(err)
	}
	m := bitgomock.New("tok")
	srv := httptest.NewServer(m)
	defer srv.Close()
	client := bitgo.New(srv.URL, "tok")
	eng := &recon.Engine{Store: s, BitGoFor: func(context.Context, uuid.UUID) (recon.BitGo, error) { return client, nil }}

	add := func(addr, value, state string) bitgo.Transfer {
		tr, err := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", Address: addr, ValueString: value, State: state})
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	// Ledger already knows T4 as pending (its confirmation webhook was lost).
	t4 := add("addr_d", "4000", "unconfirmed")
	if err := s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
		wid, err := tx.UpsertWallet(bitgo.Wallet{ID: "wlt_btc_ops", Coin: "tbtc4"})
		if err != nil {
			return err
		}
		obs, _ := domain.FromProvider(org.ID, wid, &t4)
		_, err = domain.ApplyObservation(tx, "test", obs)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	m.SetTransferState("wlt_btc_ops", t4.ID, "confirmed")

	add("addr_a", "1000", "confirmed") // webhook never arrived; expected → matched
	add("addr_b", "1999", "confirmed") // expected 2000 → amount_mismatch
	add("addr_x", "777", "confirmed")  // nobody expected it → missing_internal

	past := time.Now().Add(-time.Hour)
	if err := s.InOrgAs(ctx, org.ID, u, func(tx *store.Tx) error {
		for _, p := range []store.ExpectedPayment{
			{Reference: "INV-A", Coin: "tbtc4", Address: "addr_a", Amount: "1000"},
			{Reference: "INV-B", Coin: "tbtc4", Address: "addr_b", Amount: "2000"},
			{Reference: "INV-C", Coin: "tbtc4", Address: "addr_c", Amount: "500", ExpectedBy: &past},
			{Reference: "INV-D", Coin: "tbtc4", Address: "addr_d", Amount: "4000"},
		} {
			if _, err := tx.UpsertExpectedPayment(p); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	sum, err := eng.Run(ctx, org.ID, "manual")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"missed_webhook": 3, "status_mismatch": 1, "matched": 2, "amount_mismatch": 1, "missing_bitgo": 1, "missing_internal": 1}
	for k, v := range want {
		if sum.Stats[k] != v {
			t.Errorf("%s: want %d, got %d (stats %v)", k, v, sum.Stats[k], sum.Stats)
		}
	}

	var statuses = map[string]string{}
	s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
		eps, _ := tx.ListExpectedPayments(10)
		for _, p := range eps {
			statuses[p.Reference] = p.Status
		}
		return nil
	})
	if statuses["INV-A"] != "matched" || statuses["INV-B"] != "mismatched" || statuses["INV-C"] != "open" || statuses["INV-D"] != "matched" {
		t.Fatalf("expected payment statuses: %v", statuses)
	}

	// A second run finds nothing new and repeats nothing.
	sum2, err := eng.Run(ctx, org.ID, "schedule")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range sum2.Stats {
		if k != "transfers_checked" && v != 0 {
			t.Errorf("second run repeated %s=%d", k, v)
		}
	}
}

func TestReconFailureIsRecordedAndAlerted(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	u := storetest.CreateUser(t, owner, "ops@example.com")
	org, _, _ := s.CreateOrganizationWithOwner(ctx, "Acme", u)
	m := bitgomock.New("tok")
	srv := httptest.NewServer(m)
	defer srv.Close()
	client := bitgo.New(srv.URL, "wrong-token", bitgo.WithMaxRetries(0))
	eng := &recon.Engine{Store: s, BitGoFor: func(context.Context, uuid.UUID) (recon.BitGo, error) { return client, nil }}
	if _, err := eng.Run(ctx, org.ID, "schedule"); err == nil {
		t.Fatal("expected failure")
	}
	c := storetest.Owner(t, owner)
	var status, kind string
	c.QueryRow(ctx, `SELECT status FROM app.reconciliation_runs`).Scan(&status)
	c.QueryRow(ctx, `SELECT kind FROM app.security_alerts`).Scan(&kind)
	if status != "failed" || kind != "reconciliation_failed" {
		t.Fatalf("run status %q, alert %q", status, kind)
	}
}
