package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/vaultops/core/internal/bitgo/bitgomock"
	"github.com/vaultops/core/internal/httpapi/httpapitest"
)

func TestReconciliationAPI(t *testing.T) {
	e := httpapitest.New(t)
	alice, bob := e.User("alice@a.example"), e.User("bob@b.example")
	orgA, orgB := e.CreateOrg(alice, "Alpha"), e.CreateOrg(bob, "Beta")
	e.Call(alice, orgA, "PUT", "/v1/org/bitgo-connection", map[string]string{"token": httpapitest.BitGoToken})
	e.BitGo.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", Address: "addr_inv1", ValueString: "1000"})

	csv := "reference,coin,address,amount\nINV-1,tbtc4,addr_inv1,1000\n"
	if rec, _ := e.CallRaw(alice, orgA, "POST", "/v1/expected-payments/import", "application/json", csv); rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("non-CSV import: %d", rec.Code)
	}
	if rec, out := e.CallRaw(alice, orgA, "POST", "/v1/expected-payments/import", "text/csv", "reference,coin,address,amount\nX,tbtc4,a,1.5\n"); rec.Code != http.StatusUnprocessableEntity || out["error"] != "invalid_rows" {
		t.Fatalf("bad rows: %d %v", rec.Code, out)
	}
	if rec, out := e.CallRaw(alice, orgA, "POST", "/v1/expected-payments/import", "text/csv", csv); rec.Code != http.StatusOK || out["imported"].(float64) != 1 {
		t.Fatalf("import: %d %v", rec.Code, out)
	}

	rec, out := e.Call(alice, orgA, "POST", "/v1/reconciliation/runs", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("run: %d %s", rec.Code, rec.Body)
	}
	stats := out["stats"].(map[string]any)
	if stats["matched"].(float64) != 1 || stats["missed_webhook"].(float64) != 1 {
		t.Fatalf("stats %v", stats)
	}
	runID := out["runId"].(string)
	rec, items := e.Call(alice, orgA, "GET", "/v1/reconciliation/runs/"+runID+"/items", nil)
	if rec.Code != http.StatusOK || len(items["items"].([]any)) != 2 {
		t.Fatalf("items: %d %v", rec.Code, items)
	}

	// Bob cannot import into, run, or read Alice's reconciliation.
	if rec, _ := e.CallRaw(bob, orgA, "POST", "/v1/expected-payments/import", "text/csv", csv); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant import: %d", rec.Code)
	}
	if rec, out := e.Call(bob, orgB, "GET", "/v1/reconciliation/runs/"+runID+"/items", nil); rec.Code != http.StatusOK || len(out["items"].([]any)) != 0 {
		t.Fatalf("foreign run items must be invisible: %d %v", rec.Code, out)
	}
	if rec, _ := e.Call(bob, orgB, "POST", "/v1/reconciliation/runs", nil); rec.Code != http.StatusBadGateway {
		t.Fatalf("unconnected org run: %d", rec.Code)
	}
}
