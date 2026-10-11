package bitgo_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/bitgo/bitgomock"
)

func newClient(t *testing.T) (*bitgo.Client, *bitgomock.Server) {
	t.Helper()
	m := bitgomock.New("test-token")
	srv := httptest.NewServer(m)
	t.Cleanup(srv.Close)
	return bitgo.New(srv.URL, "test-token"), m
}

func TestListWalletsFollowsPagination(t *testing.T) {
	c, _ := newClient(t)
	ws, err := c.ListWallets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 2 || ws[0].ID != "wlt_btc_ops" || ws[1].BalanceString != "2500000000000000000" {
		t.Fatalf("unexpected wallets %+v", ws)
	}
}

func TestUnauthorizedIsNotRetried(t *testing.T) {
	m := bitgomock.New("right")
	srv := httptest.NewServer(m)
	defer srv.Close()
	_, err := bitgo.New(srv.URL, "wrong").ListWallets(context.Background())
	var be *bitgo.Error
	if !errors.As(err, &be) || be.Status != http.StatusUnauthorized || be.Retryable {
		t.Fatalf("want non-retryable 401, got %v", err)
	}
	if len(m.Requests) != 1 {
		t.Fatalf("401 should not be retried, saw %d requests", len(m.Requests))
	}
}

func TestTransientFailuresAreRetried(t *testing.T) {
	c, m := newClient(t)
	tr, err := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", ValueString: "1000"})
	if err != nil {
		t.Fatal(err)
	}
	srvFail(t, m, 2)
	got, err := c.GetTransfer(context.Background(), "tbtc4", "wlt_btc_ops", tr.ID)
	if err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if got.ValueString != "1000" {
		t.Fatalf("got %+v", got)
	}
}

func TestRetriesExhaustedReturnsRetryableError(t *testing.T) {
	m := bitgomock.New("tok")
	srv := httptest.NewServer(m)
	defer srv.Close()
	c := bitgo.New(srv.URL, "tok", bitgo.WithMaxRetries(1))
	tr, _ := m.AddTransfer(bitgomock.AddTransferRequest{WalletID: "wlt_btc_ops", ValueString: "1"})
	srvFail(t, m, 5)
	_, err := c.GetTransfer(context.Background(), "tbtc4", "wlt_btc_ops", tr.ID)
	if !bitgo.IsRetryable(err) {
		t.Fatalf("want retryable error, got %v", err)
	}
}

func srvFail(t *testing.T, m *bitgomock.Server, n int) {
	t.Helper()
	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__mock/fail", strings.NewReader(`{"Count":`+strconv.Itoa(n)+`}`)))
	if rec.Code != http.StatusOK {
		t.Fatal("failed to inject failures")
	}
}
