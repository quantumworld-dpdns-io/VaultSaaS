// Package identity verifies Sign-In with Ethereum (EIP-4361) messages used to
// LINK a wallet to an existing VaultOps user.
//
// Decision D4: a wallet is a linked identity only. Linking requires an
// already-authenticated user (email/OAuth via Supabase) and never creates an
// account or a session. The same signature primitive is reused in Phase 2 as
// the step-up proof for approvals.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// Statement is shown in the wallet; it states plainly what signing does.
const Statement = "Link this wallet to your VaultOps account. This does not sign you in and cannot move funds."

var (
	ErrMalformed    = errors.New("identity: malformed SIWE message")
	ErrDomain       = errors.New("identity: SIWE domain mismatch")
	ErrExpired      = errors.New("identity: SIWE message expired or not yet valid")
	ErrSignature    = errors.New("identity: signature does not match address")
	ErrNonce        = errors.New("identity: nonce unknown, used or expired")
	ErrBadChecksum  = errors.New("identity: address fails EIP-55 checksum")
	ErrWrongPurpose = errors.New("identity: unexpected statement")
)

type SIWE struct {
	Domain string        // e.g. app.vaultops.example (host[:port] of the dashboard)
	MaxAge time.Duration // accepted age of Issued At
	Now    func() time.Time
}

type Message struct {
	Domain    string
	Address   string // as written in the message (EIP-55)
	Statement string
	URI       string
	Version   string
	ChainID   int64
	Nonce     string
	IssuedAt  time.Time
	ExpiresAt *time.Time
}

// NewNonce returns an alphanumeric nonce (EIP-4361 requires >= 8 chars).
func NewNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func ParseMessage(s string) (*Message, error) {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	if len(lines) < 7 {
		return nil, ErrMalformed
	}
	const suffix = " wants you to sign in with your Ethereum account:"
	if !strings.HasSuffix(lines[0], suffix) {
		return nil, ErrMalformed
	}
	m := &Message{Domain: strings.TrimSuffix(lines[0], suffix), Address: lines[1]}
	if lines[2] != "" {
		return nil, ErrMalformed
	}
	i := 3
	if !strings.HasPrefix(lines[i], "URI: ") { // optional statement block
		m.Statement = lines[i]
		i++
		if i >= len(lines) || lines[i] != "" {
			return nil, ErrMalformed
		}
		i++
	}
	fields := map[string]string{}
	for ; i < len(lines); i++ {
		if lines[i] == "" {
			continue
		}
		k, v, ok := strings.Cut(lines[i], ": ")
		if !ok {
			return nil, ErrMalformed
		}
		fields[k] = v
	}
	m.URI, m.Version, m.Nonce = fields["URI"], fields["Version"], fields["Nonce"]
	var err error
	if m.ChainID, err = strconv.ParseInt(fields["Chain ID"], 10, 64); err != nil {
		return nil, ErrMalformed
	}
	if m.IssuedAt, err = time.Parse(time.RFC3339, fields["Issued At"]); err != nil {
		return nil, ErrMalformed
	}
	if e, ok := fields["Expiration Time"]; ok {
		t, err := time.Parse(time.RFC3339, e)
		if err != nil {
			return nil, ErrMalformed
		}
		m.ExpiresAt = &t
	}
	if m.URI == "" || m.Version != "1" || len(m.Nonce) < 8 {
		return nil, ErrMalformed
	}
	return m, nil
}

// Verify checks the message and signature and returns the lowercase address.
// consumeNonce must atomically mark the caller's nonce as used.
func (v *SIWE) Verify(raw, signatureHex string, consumeNonce func(string) (bool, error)) (string, error) {
	m, err := ParseMessage(raw)
	if err != nil {
		return "", err
	}
	if m.Domain != v.Domain {
		return "", ErrDomain
	}
	if m.Statement != Statement {
		return "", ErrWrongPurpose
	}
	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	maxAge := v.MaxAge
	if maxAge == 0 {
		maxAge = 10 * time.Minute
	}
	t := now()
	if m.IssuedAt.After(t.Add(time.Minute)) || t.Sub(m.IssuedAt) > maxAge || (m.ExpiresAt != nil && !t.Before(*m.ExpiresAt)) {
		return "", ErrExpired
	}
	if !ValidChecksum(m.Address) {
		return "", ErrBadChecksum
	}
	signer, err := RecoverPersonalSign([]byte(raw), signatureHex)
	if err != nil {
		return "", err
	}
	if signer != strings.ToLower(m.Address) {
		return "", ErrSignature
	}
	ok, err := consumeNonce(m.Nonce)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNonce
	}
	return signer, nil
}

func keccak(b ...[]byte) []byte {
	h := sha3.NewLegacyKeccak256()
	for _, x := range b {
		h.Write(x)
	}
	return h.Sum(nil)
}

// RecoverPersonalSign returns the lowercase 0x address that produced an
// EIP-191 personal_sign signature (r||s||v, v in {0,1,27,28}).
func RecoverPersonalSign(msg []byte, signatureHex string) (string, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(signatureHex, "0x"))
	if err != nil || len(sig) != 65 {
		return "", ErrSignature
	}
	v := sig[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return "", ErrSignature
	}
	digest := keccak([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(msg))), msg)
	compact := append([]byte{27 + v}, sig[:64]...)
	pub, _, err := ecdsa.RecoverCompact(compact, digest)
	if err != nil {
		return "", ErrSignature
	}
	return AddressFromPubKey(pub.SerializeUncompressed()), nil
}

// AddressFromPubKey derives the lowercase 0x address from a 65-byte public key.
func AddressFromPubKey(uncompressed []byte) string {
	return "0x" + hex.EncodeToString(keccak(uncompressed[1:])[12:])
}

// ValidChecksum validates EIP-55 mixed-case checksums (all-lowercase or
// all-uppercase addresses carry no checksum and are accepted).
func ValidChecksum(addr string) bool {
	if len(addr) != 42 || !strings.HasPrefix(addr, "0x") {
		return false
	}
	body := addr[2:]
	if _, err := hex.DecodeString(body); err != nil {
		return false
	}
	if body == strings.ToLower(body) || body == strings.ToUpper(body) {
		return true
	}
	return Checksum(body) == addr
}

// Checksum returns the EIP-55 form of a 40-hex-char address body.
func Checksum(body string) string {
	lower := strings.ToLower(body)
	h := hex.EncodeToString(keccak([]byte(lower)))
	out := []byte(lower)
	for i := range out {
		if out[i] >= 'a' && h[i] >= '8' {
			out[i] -= 32
		}
	}
	return "0x" + string(out)
}
