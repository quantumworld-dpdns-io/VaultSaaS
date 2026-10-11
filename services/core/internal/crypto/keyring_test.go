package crypto

import (
	"bytes"
	"testing"
)

func ring(t *testing.T, spec string) *Keyring {
	t.Helper()
	k, err := ParseKeyring(spec)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestSealOpenAndBinding(t *testing.T) {
	k1, _ := NewKey()
	k := ring(t, "1:"+k1)
	sealed, ver, err := k.Seal([]byte("v2x-token"), []byte("org-a"))
	if err != nil || ver != 1 {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("v2x-token")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	got, err := k.Open(sealed, ver, []byte("org-a"))
	if err != nil || string(got) != "v2x-token" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := k.Open(sealed, ver, []byte("org-b")); err == nil {
		t.Fatal("a token sealed for one org must not open for another")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := k.Open(sealed, ver, []byte("org-a")); err == nil {
		t.Fatal("tampered ciphertext must not open")
	}
}

func TestRotationKeepsOldVersionsReadable(t *testing.T) {
	a, _ := NewKey()
	b, _ := NewKey()
	old := ring(t, "1:"+a)
	sealed, v, _ := old.Seal([]byte("secret"), nil)
	rotated := ring(t, "1:"+a+",2:"+b)
	if got, err := rotated.Open(sealed, v, nil); err != nil || string(got) != "secret" {
		t.Fatal("old version must stay readable after rotation")
	}
	if _, v2, _ := rotated.Seal([]byte("x"), nil); v2 != 2 {
		t.Fatalf("new data must use the newest key, got v%d", v2)
	}
}

func TestParseKeyringRejectsBadInput(t *testing.T) {
	for _, bad := range []string{"", "nokey", "0:AAAA", "1:short", "x:" + "A"} {
		if _, err := ParseKeyring(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
