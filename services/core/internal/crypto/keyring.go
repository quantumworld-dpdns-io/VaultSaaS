// Package crypto seals secrets stored in the database (BitGo access tokens)
// with AES-256-GCM. Keys are versioned so they can be rotated: new data uses
// the highest version, old rows stay readable until re-sealed.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Keyring struct {
	keys    map[int]cipher.AEAD
	current int
}

var ErrUnknownKey = errors.New("crypto: unknown key version")

// ParseKeyring reads "1:<base64 32 bytes>,2:<base64 32 bytes>".
func ParseKeyring(spec string) (*Keyring, error) {
	kr := &Keyring{keys: map[int]cipher.AEAD{}}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, b64, ok := strings.Cut(part, ":")
		if !ok {
			return nil, fmt.Errorf("crypto: key entry must be <version>:<base64>")
		}
		ver, err := strconv.Atoi(v)
		if err != nil || ver < 1 {
			return nil, fmt.Errorf("crypto: invalid key version %q", v)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("crypto: key %d must be 32 bytes of base64", ver)
		}
		block, _ := aes.NewCipher(raw)
		aead, _ := cipher.NewGCM(block)
		kr.keys[ver] = aead
		kr.current = max(kr.current, ver)
	}
	if kr.current == 0 {
		return nil, errors.New("crypto: no keys configured")
	}
	return kr, nil
}

// Seal encrypts plaintext bound to aad (e.g. the owning org id), returning
// nonce||ciphertext and the key version used.
func (k *Keyring) Seal(plaintext, aad []byte) ([]byte, int, error) {
	aead := k.keys[k.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, 0, err
	}
	return aead.Seal(nonce, nonce, plaintext, aad), k.current, nil
}

func (k *Keyring) Open(sealed []byte, version int, aad []byte) ([]byte, error) {
	aead, ok := k.keys[version]
	if !ok {
		return nil, ErrUnknownKey
	}
	if len(sealed) < aead.NonceSize() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	return aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
}

// NewKey returns a fresh base64 key for operators (vaultctl keygen).
func NewKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
