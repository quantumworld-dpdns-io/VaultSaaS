package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/vaultops/core/internal/httpapi/httpapitest"
)

func TestAlertSettingsAndResolve(t *testing.T) {
	e := httpapitest.New(t)
	alice, bob := e.User("alice@a.example"), e.User("bob@b.example")
	orgA, orgB := e.CreateOrg(alice, "Alpha"), e.CreateOrg(bob, "Beta")

	rec, out := e.Call(alice, orgA, "GET", "/v1/org/alert-settings", nil)
	if rec.Code != http.StatusOK || out["dlqThreshold"].(float64) != 3 {
		t.Fatalf("defaults: %d %v", rec.Code, out)
	}
	for _, bad := range []map[string]any{
		{"largeTransfer": map[string]string{"tbtc4": "1.5"}, "dlqThreshold": 3, "tokenWarnDays": 14},
		{"largeTransfer": map[string]string{"TB TC": "1"}, "dlqThreshold": 3, "tokenWarnDays": 14},
		{"largeTransfer": map[string]string{}, "dlqThreshold": 0, "tokenWarnDays": 14},
	} {
		if rec, _ := e.Call(alice, orgA, "PUT", "/v1/org/alert-settings", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%v accepted: %d", bad, rec.Code)
		}
	}
	good := map[string]any{"largeTransfer": map[string]string{"tbtc4": "100000000"}, "dlqThreshold": 5, "tokenWarnDays": 30}
	if rec, _ := e.Call(alice, orgA, "PUT", "/v1/org/alert-settings", good); rec.Code != http.StatusOK {
		t.Fatalf("put: %d", rec.Code)
	}
	if rec, _ := e.Call(bob, orgA, "PUT", "/v1/org/alert-settings", good); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-tenant settings: %d", rec.Code)
	}

	// An expiring token raises an alert on connect + scheduled run; resolve it.
	exp := time.Now().Add(48 * time.Hour)
	if rec, out := e.Call(alice, orgA, "PUT", "/v1/org/bitgo-connection",
		map[string]any{"token": httpapitest.BitGoToken, "tokenExpiresAt": exp}); rec.Code != http.StatusOK {
		t.Fatalf("connect: %d %v", rec.Code, out)
	}
	e.RunScheduledRules(orgA)
	_, alerts := e.Call(alice, orgA, "GET", "/v1/alerts", nil)
	list := alerts["alerts"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["kind"] != "bitgo_token_expiring" {
		t.Fatalf("alerts: %v", list)
	}
	id := list[0].(map[string]any)["id"].(string)
	if rec, _ := e.Call(bob, orgB, "POST", "/v1/alerts/"+id+"/resolve", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("resolving another tenant's alert must look like not found: %d", rec.Code)
	}
	if rec, _ := e.Call(alice, orgA, "POST", "/v1/alerts/"+id+"/resolve", nil); rec.Code != http.StatusOK {
		t.Fatalf("resolve: %d", rec.Code)
	}
	if rec, _ := e.Call(alice, orgA, "POST", "/v1/alerts/"+id+"/resolve", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("second resolve: %d", rec.Code)
	}
}
