// Package siwetest signs SIWE messages with throwaway keys for tests and the
// Robot suites. Never used by deployed code.
package siwetest

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/vaultops/core/internal/identity"
	"golang.org/x/crypto/sha3"
)

type Key struct{ priv *secp256k1.PrivateKey }

func NewKey() Key {
	k, err := secp256k1.GeneratePrivateKey()
	if err != nil {
		panic(err)
	}
	return Key{k}
}

func KeyFromHex(h string) (Key, error) {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 32 {
		return Key{}, fmt.Errorf("siwetest: key must be 32 bytes hex")
	}
	return Key{secp256k1.PrivKeyFromBytes(b)}, nil
}

func (k Key) Hex() string { return hex.EncodeToString(k.priv.Serialize()) }

// Address returns the EIP-55 checksummed address.
func (k Key) Address() string {
	lower := identity.AddressFromPubKey(k.priv.PubKey().SerializeUncompressed())
	return identity.Checksum(lower[2:])
}

// SignPersonal produces an EIP-191 personal_sign signature (r||s||v, v=27/28).
func (k Key) SignPersonal(msg string) string {
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msg))))
	h.Write([]byte(msg))
	compact := ecdsa.SignCompact(k.priv, h.Sum(nil), false) // [27+recid][r][s]
	sig := append(append([]byte{}, compact[1:]...), compact[0])
	return "0x" + hex.EncodeToString(sig)
}

// Message builds a SIWE message the way the dashboard does.
func Message(domain, address, nonce string, issued time.Time) string {
	return fmt.Sprintf("%s wants you to sign in with your Ethereum account:\n%s\n\n%s\n\nURI: https://%s\nVersion: 1\nChain ID: 1\nNonce: %s\nIssued At: %s",
		domain, address, identity.Statement, domain, nonce, issued.UTC().Format(time.RFC3339))
}
