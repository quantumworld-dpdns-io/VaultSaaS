package bitgo

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

// D5: no endpoint that builds, signs or sends funds may ever be called.
func TestGuardBlocksFundMovement(t *testing.T) {
	blocked := []struct{ method, path string }{
		{http.MethodPost, "/api/v2/tbtc4/wallet/w1/sendcoins"},
		{http.MethodPost, "/api/v2/tbtc4/wallet/w1/sendmany"},
		{http.MethodPost, "/api/v2/tbtc4/wallet/w1/tx/build"},
		{http.MethodPost, "/api/v2/tbtc4/wallet/w1/tx/send"},
		{http.MethodPost, "/api/v2/tbtc4/wallet/w1/tx/initiate"},
		{http.MethodGet, "/api/v2/tbtc4/key/k1"},
		{http.MethodPost, "/api/v2/tbtc4/wallet/w1/consolidateunspents"},
		{http.MethodPost, "/api/v2/wallets"},
		{http.MethodDelete, "/api/v2/tbtc4/wallet/w1"},
		{http.MethodPut, "/api/v2/tbtc4/wallet/w1"},
	}
	for _, b := range blocked {
		if err := guard(b.method, b.path); !errors.Is(err, ErrForbiddenOperation) {
			t.Errorf("%s %s should be forbidden", b.method, b.path)
		}
	}
	allowed := []struct{ method, path string }{
		{http.MethodGet, "/api/v2/wallets"},
		{http.MethodGet, "/api/v2/tbtc4/wallet/w1/transfer/t1"},
		{http.MethodPut, "/api/v2/pendingapprovals/pa1"},
	}
	for _, a := range allowed {
		if err := guard(a.method, a.path); err != nil {
			t.Errorf("%s %s should be allowed: %v", a.method, a.path, err)
		}
	}
}

func TestForbiddenRequestNeverLeavesProcess(t *testing.T) {
	c := New("http://127.0.0.1:1", "tok") // nothing listens here
	err := c.do(context.Background(), http.MethodPost, "/api/v2/tbtc4/wallet/w1/sendcoins", nil, map[string]string{"amount": "1"}, nil)
	if !errors.Is(err, ErrForbiddenOperation) {
		t.Fatalf("want ErrForbiddenOperation, got %v", err)
	}
	if IsRetryable(err) {
		t.Fatal("forbidden operations must not be retried")
	}
}
