// Command mockbitgo runs the fake BitGo API for local development and the
// Robot Framework suites. Never deployed.
package main

import (
	"log"
	"net/http"
	"os"

	"github.com/vaultops/core/internal/bitgo/bitgomock"
)

func main() {
	addr := envOr("MOCK_BITGO_ADDR", "127.0.0.1:8090")
	token := envOr("MOCK_BITGO_TOKEN", "local-dev-token")
	log.Printf("mock BitGo listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, bitgomock.New(token)))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
