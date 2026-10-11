package config

import (
	"context"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestLoadDefaultsToBitGoTestnet(t *testing.T) {
	c, err := load(context.Background(), env(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.BitGoBaseURL != BitGoTestURL || c.Env != "local" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoadResolvesSecretFromSSM(t *testing.T) {
	fetch := func(_ context.Context, name string) (string, error) {
		if name != "/vaultops/dev/bitgo-token" {
			t.Fatalf("unexpected parameter %q", name)
		}
		return "v2x-secret", nil
	}
	c, err := load(context.Background(), env(map[string]string{"BITGO_TOKEN_SSM": "/vaultops/dev/bitgo-token"}), fetch)
	if err != nil {
		t.Fatal(err)
	}
	if c.BitGoToken != "v2x-secret" {
		t.Fatalf("token not resolved: %q", c.BitGoToken)
	}
}

func TestProdBitGoURLRequiresProdEnv(t *testing.T) {
	_, err := load(context.Background(), env(map[string]string{"VAULTOPS_ENV": "dev", "BITGO_BASE_URL": BitGoProdURL}), nil)
	if err == nil {
		t.Fatal("expected error when dev points at BitGo production")
	}
}
