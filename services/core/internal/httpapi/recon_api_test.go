package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vaultops/core/internal/bitgo/bitgomock"
	"github.com/vaultops/core/internal/httpapi/httpapitest"
	"github.com/vaultops/core/internal/report"
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

	// Daily report: CSV locally, viewer access, date validated.
	today := time.Now().In(report.Taipei).Format("2006-01-02")
	rec = httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/reports/daily?date="+today, nil)
	req.Header.Set("X-Vaultops-User-Id", alice.ID.String())
	req.Header.Set("X-Vaultops-Org-Id", orgA)
	e.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") ||
		!strings.Contains(rec.Body.String(), "missing_internal") && !strings.Contains(rec.Body.String(), "transfer,") {
		t.Fatalf("report: %d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	for _, bad := range []string{"2999-01-01", "yesterday", "2020-01-01"} {
		if rec, _ := e.Call(alice, orgA, "GET", "/v1/reports/daily?date="+bad, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("date %s: %d", bad, rec.Code)
		}
	}
	if rec, _ := e.Call(bob, orgA, "GET", "/v1/reports/daily?date="+today, nil); rec.Code != http.StatusForbidden {
		t.Errorf("cross-tenant report: %d", rec.Code)
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
