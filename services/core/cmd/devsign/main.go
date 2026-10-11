// Command devsign signs a VaultOps wallet-link (SIWE) message with a test key,
// standing in for a browser wallet in the Robot suites. Never deployed.
//
//	devsign -domain 127.0.0.1:15173 -nonce <nonce> [-key <hex>]
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	"github.com/vaultops/core/internal/identity/siwetest"
)

func main() {
	domain := flag.String("domain", "", "dashboard host[:port]")
	nonce := flag.String("nonce", "", "nonce from POST /v1/me/wallets/nonce")
	keyHex := flag.String("key", "", "32-byte hex private key (random if empty)")
	flag.Parse()
	if *domain == "" || *nonce == "" {
		log.Fatal("-domain and -nonce are required")
	}
	k := siwetest.NewKey()
	if *keyHex != "" {
		var err error
		if k, err = siwetest.KeyFromHex(*keyHex); err != nil {
			log.Fatal(err)
		}
	}
	msg := siwetest.Message(*domain, k.Address(), *nonce, time.Now())
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"address": k.Address(), "message": msg, "signature": k.SignPersonal(msg)})
}
