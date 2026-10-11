package identity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/vaultops/core/internal/identity"
	"github.com/vaultops/core/internal/identity/siwetest"
)

const domain = "app.vaultops.example"

func consumeOnce() func(string) (bool, error) {
	used := map[string]bool{}
	return func(n string) (bool, error) {
		if used[n] {
			return false, nil
		}
		used[n] = true
		return true, nil
	}
}

func TestChecksumVector(t *testing.T) {
	// EIP-55 reference vector
	const addr = "0x5aAeb6053F3E94C9b9A09f33669435E7Ef1BeAed"
	if identity.Checksum(strings.ToLower(addr[2:])) != addr || !identity.ValidChecksum(addr) {
		t.Fatal("EIP-55 vector failed")
	}
	if identity.ValidChecksum("0x5aaeb6053F3E94C9b9A09f33669435E7Ef1BeAed") {
		t.Fatal("bad checksum accepted")
	}
}

func TestVerifyHappyPathAndReplay(t *testing.T) {
	k := siwetest.NewKey()
	v := &identity.SIWE{Domain: domain}
	msg := siwetest.Message(domain, k.Address(), "abcdef0123456789", time.Now())
	consume := consumeOnce()
	addr, err := v.Verify(msg, k.SignPersonal(msg), consume)
	if err != nil || addr != strings.ToLower(k.Address()) {
		t.Fatalf("verify: %s %v", addr, err)
	}
	if _, err := v.Verify(msg, k.SignPersonal(msg), consume); err != identity.ErrNonce {
		t.Fatalf("replay must fail on the nonce, got %v", err)
	}
}

func TestVerifyRejections(t *testing.T) {
	k, other := siwetest.NewKey(), siwetest.NewKey()
	v := &identity.SIWE{Domain: domain}
	good := siwetest.Message(domain, k.Address(), "abcdef0123456789", time.Now())
	cases := map[string]struct {
		msg, sig string
		want     error
	}{
		"signed by another key": {good, other.SignPersonal(good), identity.ErrSignature},
		"phishing domain":       {siwetest.Message("evil.example", k.Address(), "abcdef0123456789", time.Now()), "", identity.ErrDomain},
		"stale":                 {siwetest.Message(domain, k.Address(), "abcdef0123456789", time.Now().Add(-time.Hour)), "", identity.ErrExpired},
		"wrong purpose":         {strings.Replace(good, identity.Statement, "Sign in to VaultOps", 1), "", identity.ErrWrongPurpose},
		"garbage":               {"hello", "0x00", identity.ErrMalformed},
	}
	for name, c := range cases {
		sig := c.sig
		if sig == "" {
			sig = k.SignPersonal(c.msg)
		}
		if _, err := v.Verify(c.msg, sig, consumeOnce()); err != c.want {
			t.Errorf("%s: want %v, got %v", name, c.want, err)
		}
	}
}
