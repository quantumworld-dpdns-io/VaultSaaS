package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/httpapi/httpapitest"
	"github.com/vaultops/core/internal/store/storetest"
)

func TestEndpointsRequireAuthentication(t *testing.T) {
	e := httpapitest.New(t)
	for _, path := range []string{"/v1/me", "/v1/wallets", "/v1/transfers", "/v1/audit"} {
		if rec, _ := e.Call(httpapitest.User{}, uuid.NewString(), http.MethodGet, path, nil); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s anonymous: %d", path, rec.Code)
		}
	}
}

// BOLA/IDOR: a member of A gets the same 403 for B's org id as for an org
// that does not exist, on every org-scoped endpoint.
func TestCrossTenantAccessIsDenied(t *testing.T) {
	e := httpapitest.New(t)
	alice, bob := e.User("alice@a.example"), e.User("bob@b.example")
	orgA, orgB := e.CreateOrg(alice, "Alpha"), e.CreateOrg(bob, "Beta")

	type ep struct{ method, path string }
	endpoints := []ep{
		{"GET", "/v1/wallets"}, {"GET", "/v1/transfers"}, {"GET", "/v1/alerts"}, {"GET", "/v1/audit"},
		{"GET", "/v1/org/members"}, {"POST", "/v1/org/invitations"}, {"PUT", "/v1/org/bitgo-connection"},
	}
	body := map[string]string{"email": "x@x.example", "role": "viewer", "token": httpapitest.BitGoToken}
	for _, target := range []string{orgB, uuid.NewString()} {
		for _, x := range endpoints {
			rec, out := e.Call(alice, target, x.method, x.path, body)
			if rec.Code != http.StatusForbidden || out["error"] != "forbidden" {
				t.Errorf("alice %s %s on %s: %d %v", x.method, x.path, target, rec.Code, out)
			}
		}
	}
	if rec, _ := e.Call(alice, orgA, "GET", "/v1/transfers", nil); rec.Code != http.StatusOK {
		t.Fatalf("own org must work: %d", rec.Code)
	}
	if rec, _ := e.Call(alice, "not-a-uuid", "GET", "/v1/transfers", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed org id: %d", rec.Code)
	}
}

func TestRoleBasedAccess(t *testing.T) {
	e := httpapitest.New(t)
	owner, carol := e.User("owner@a.example"), e.User("carol@a.example")
	org := e.CreateOrg(owner, "Alpha")

	rec, out := e.Call(owner, org, "POST", "/v1/org/invitations", map[string]string{"email": "carol@a.example", "role": "viewer"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := e.Call(carol, nil, "POST", "/v1/invitations/accept", map[string]string{"token": out["token"].(string)}); rec.Code != http.StatusOK {
		t.Fatalf("accept: %d %s", rec.Code, rec.Body)
	}
	// viewer can read …
	for _, p := range []string{"/v1/transfers", "/v1/alerts", "/v1/org/members"} {
		if rec, _ := e.Call(carol, org, "GET", p, nil); rec.Code != http.StatusOK {
			t.Errorf("viewer GET %s: %d", p, rec.Code)
		}
	}
	// … but not administer.
	if rec, _ := e.Call(carol, org, "GET", "/v1/audit", nil); rec.Code != http.StatusForbidden {
		t.Errorf("viewer audit: %d", rec.Code)
	}
	if rec, _ := e.Call(carol, org, "POST", "/v1/org/invitations", map[string]string{"email": "d@a.example", "role": "viewer"}); rec.Code != http.StatusForbidden {
		t.Errorf("viewer invite: %d", rec.Code)
	}
	if rec, _ := e.Call(carol, org, "PUT", "/v1/org/bitgo-connection", map[string]string{"token": httpapitest.BitGoToken}); rec.Code != http.StatusForbidden {
		t.Errorf("viewer connect: %d", rec.Code)
	}
	// Nobody mints owners by invitation.
	if rec, out := e.Call(owner, org, "POST", "/v1/org/invitations", map[string]string{"email": "e@a.example", "role": "owner"}); rec.Code != http.StatusForbidden || out["error"] != "cannot_grant_role" {
		t.Errorf("owner invite: %d %v", rec.Code, out)
	}
	// An invitation cannot be redeemed by someone else.
	_, out = e.Call(owner, org, "POST", "/v1/org/invitations", map[string]string{"email": "dave@a.example", "role": "admin"})
	if rec, _ := e.Call(carol, nil, "POST", "/v1/invitations/accept", map[string]string{"token": out["token"].(string)}); rec.Code != http.StatusForbidden {
		t.Errorf("stolen invitation: %d", rec.Code)
	}
}

func TestBitGoConnectionIsPerOrgAndEncrypted(t *testing.T) {
	e := httpapitest.New(t)
	alice, bob := e.User("alice@a.example"), e.User("bob@b.example")
	orgA, orgB := e.CreateOrg(alice, "Alpha"), e.CreateOrg(bob, "Beta")

	if rec, out := e.Call(alice, orgA, "GET", "/v1/wallets", nil); rec.Code != http.StatusConflict || out["error"] != "bitgo_not_connected" {
		t.Fatalf("before connecting: %d %v", rec.Code, out)
	}
	if rec, out := e.Call(alice, orgA, "PUT", "/v1/org/bitgo-connection", map[string]string{"token": "wrong-token"}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad token: %d %v", rec.Code, out)
	}
	if rec, out := e.Call(alice, orgA, "PUT", "/v1/org/bitgo-connection", map[string]string{"token": httpapitest.BitGoToken, "enterpriseId": "ent_demo"}); rec.Code != http.StatusOK || out["walletsVisible"].(float64) != 2 {
		t.Fatalf("connect: %d %v", rec.Code, out)
	}
	rec, out := e.Call(alice, orgA, "GET", "/v1/wallets", nil)
	if rec.Code != http.StatusOK || len(out["wallets"].([]any)) != 2 {
		t.Fatalf("wallets: %d %v", rec.Code, out)
	}
	// Bob's org did not inherit Alice's connection.
	if rec, _ := e.Call(bob, orgB, "GET", "/v1/wallets", nil); rec.Code != http.StatusConflict {
		t.Fatalf("org B must not see org A's BitGo: %d", rec.Code)
	}
	// The token is stored sealed.
	c := storetest.Owner(t, e.Owner)
	var sealed []byte
	c.QueryRow(context.Background(), `SELECT encrypted_token FROM app.bitgo_connections`).Scan(&sealed)
	if len(sealed) == 0 || bytes.Contains(sealed, []byte(httpapitest.BitGoToken)) {
		t.Fatal("BitGo token must be encrypted at rest")
	}
	// And the audit chain recorded the change and verifies.
	rec, out = e.Call(alice, orgA, "GET", "/v1/audit", nil)
	if rec.Code != http.StatusOK || out["chainIntact"] != true {
		t.Fatalf("audit: %d %v", rec.Code, out)
	}
	actions := map[string]bool{}
	for _, en := range out["entries"].([]any) {
		actions[en.(map[string]any)["action"].(string)] = true
	}
	if !actions["organization.created"] || !actions["bitgo_connection.updated"] {
		t.Fatalf("audit actions: %v", actions)
	}
}
