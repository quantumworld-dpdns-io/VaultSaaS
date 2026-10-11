package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/bitgo/bitgomock"
)

func TestListWalletsReturnsBalancesAsStrings(t *testing.T) {
	mock := httptest.NewServer(bitgomock.New("tok"))
	defer mock.Close()
	s := New(Deps{Wallets: bitgo.New(mock.URL, "tok")})

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/wallets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body struct {
		Wallets []map[string]any `json:"wallets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Wallets) != 2 {
		t.Fatalf("want 2 wallets, got %d", len(body.Wallets))
	}
	if _, ok := body.Wallets[1]["balance"].(string); !ok {
		t.Fatal("balance must be a string, never a float")
	}
}

func TestListWalletsHidesUpstreamErrors(t *testing.T) {
	mock := httptest.NewServer(bitgomock.New("tok"))
	defer mock.Close()
	s := New(Deps{Wallets: bitgo.New(mock.URL, "wrong-token")})
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/wallets", nil))
	if rec.Code != http.StatusBadGateway || rec.Body.String() != "{\"error\":\"bitgo_unavailable\"}\n" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}
