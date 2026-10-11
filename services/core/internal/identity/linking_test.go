package identity_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/vaultops/core/internal/httpapi/httpapitest"
	"github.com/vaultops/core/internal/identity/siwetest"
	"github.com/vaultops/core/internal/store/storetest"
)

// Decision D4: a wallet is a linked identity only. It can be attached to an
// already-authenticated user, but can never authenticate or create one.
func TestWalletCannotCreateAccount(t *testing.T) {
	e := httpapitest.New(t)
	ctx := context.Background()
	owner := storetest.Owner(t, e.Owner)
	countUsers := func() int {
		var n int
		owner.QueryRow(ctx, `SELECT count(*) FROM auth.users`).Scan(&n)
		return n
	}
	k := siwetest.NewKey()
	msg := siwetest.Message(httpapitest.SIWEDomain, k.Address(), "0123456789abcdef", time.Now())
	body := map[string]string{"message": msg, "signature": k.SignPersonal(msg)}

	// Anonymous: a valid wallet signature is not a credential.
	before := countUsers()
	for _, path := range []string{"/v1/me/wallets", "/v1/me/wallets/nonce"} {
		if rec, _ := e.Call(httpapitest.User{}, nil, http.MethodPost, path, body); rec.Code != http.StatusUnauthorized {
			t.Errorf("anonymous POST %s: %d", path, rec.Code)
		}
	}
	// There is no wallet sign-in / sign-up route at all.
	for _, path := range []string{"/v1/auth/wallet", "/v1/siwe/login", "/v1/signup/wallet"} {
		if rec, _ := e.Call(httpapitest.User{}, nil, http.MethodPost, path, body); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s must not exist: %d", path, rec.Code)
		}
	}
	if countUsers() != before {
		t.Fatal("wallet requests created a user")
	}

	// Authenticated linking works and is bound to that user.
	alice := e.User("alice@a.example")
	_, nonce := e.Call(alice, nil, http.MethodPost, "/v1/me/wallets/nonce", nil)
	msg = siwetest.Message(httpapitest.SIWEDomain, k.Address(), nonce["nonce"].(string), time.Now())
	rec, out := e.Call(alice, nil, http.MethodPost, "/v1/me/wallets", map[string]string{"message": msg, "signature": k.SignPersonal(msg)})
	if rec.Code != http.StatusCreated || out["address"] != strings.ToLower(k.Address()) {
		t.Fatalf("link: %d %v", rec.Code, out)
	}
	// Replaying the same signed message fails (single-use nonce).
	if rec, _ := e.Call(alice, nil, http.MethodPost, "/v1/me/wallets", map[string]string{"message": msg, "signature": k.SignPersonal(msg)}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("replay: %d", rec.Code)
	}
	// Another user cannot claim the same wallet, nor use Alice's nonce.
	bob := e.User("bob@b.example")
	_, n2 := e.Call(bob, nil, http.MethodPost, "/v1/me/wallets/nonce", nil)
	m2 := siwetest.Message(httpapitest.SIWEDomain, k.Address(), n2["nonce"].(string), time.Now())
	if rec, out := e.Call(bob, nil, http.MethodPost, "/v1/me/wallets", map[string]string{"message": m2, "signature": k.SignPersonal(m2)}); rec.Code != http.StatusConflict {
		t.Fatalf("second owner: %d %v", rec.Code, out)
	}
	_, me := e.Call(alice, nil, http.MethodGet, "/v1/me", nil)
	if ws := me["linkedWallets"].([]any); len(ws) != 1 {
		t.Fatalf("me.linkedWallets: %v", ws)
	}
	if countUsers() != before+2 {
		t.Fatal("only the two email users should exist")
	}
}
