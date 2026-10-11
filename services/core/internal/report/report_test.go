package report_test

import (
	"context"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/domain"
	"github.com/vaultops/core/internal/objects"
	"github.com/vaultops/core/internal/report"
	"github.com/vaultops/core/internal/store"
	"github.com/vaultops/core/internal/store/storetest"
)

func TestSafeCell(t *testing.T) {
	cases := map[string]string{
		"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")", "+1+1": "'+1+1", "@SUM(A1)": "'@SUM(A1)", "-cmd": "'-cmd",
		"-1500": "-1500", "2500000000000000000": "2500000000000000000", "tb1qabc": "tb1qabc", "": "",
	}
	for in, want := range cases {
		if got := report.SafeCell(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestDailyReport(t *testing.T) {
	s, owner := storetest.New(t)
	ctx := context.Background()
	u := storetest.CreateUser(t, owner, "a@example.com")
	org, _, _ := s.CreateOrganizationWithOwner(ctx, "Acme", u)
	day := time.Date(2026, 10, 10, 12, 0, 0, 0, report.Taipei)
	s.InOrg(ctx, org.ID, func(tx *store.Tx) error {
		wid, _ := tx.UpsertWallet(bitgo.Wallet{ID: "w1", Coin: "tbtc4"})
		for i, d := range []time.Time{day, day.Add(-24 * time.Hour)} {
			obs, _ := domain.FromProvider(org.ID, wid, &bitgo.Transfer{ID: []string{"=evil()", "old"}[i], Coin: "tbtc4", Type: "send",
				ValueString: "-1500", State: "confirmed", Date: d})
			if _, err := domain.ApplyObservation(tx, "test", obs); err != nil {
				return err
			}
		}
		return nil
	})
	dir := objects.Dir{Root: t.TempDir()}
	key, body, err := report.Publish(ctx, s, dir, org.ID, day)
	if err != nil {
		t.Fatal(err)
	}
	if key != "reports/"+org.ID.String()+"/2026-10-10.csv" {
		t.Fatalf("key %s", key)
	}
	recs, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[1][2] != "'=evil()" || recs[1][5] != "-1500" {
		t.Fatalf("report rows: %v", recs)
	}
	// Reports may be regenerated (not write-once).
	if _, _, err := report.Publish(ctx, s, dir, org.ID, day); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
}
