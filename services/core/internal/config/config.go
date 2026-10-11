// Package config loads runtime configuration.
//
// Plain values come from environment variables. Secrets are never put in
// Lambda environment variables directly: in AWS the env var holds the name of
// an SSM Parameter Store SecureString (Standard tier, aws/ssm key — both free),
// e.g. BITGO_TOKEN_SSM=/vaultops/prod/bitgo-token. Locally the plain variable
// (BITGO_TOKEN) may be used instead.
package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

type Config struct {
	Env          string // dev | prod | local
	BitGoBaseURL string
	// BitGoToken is a LOCAL-ONLY fallback. Deployed environments use each
	// organization's own encrypted connection (tenant isolation).
	BitGoToken  string
	DatabaseURL string // Supavisor transaction pooler URL for role vaultops_app
	// TokenEncKeys is the keyring for sealing stored BitGo tokens: "1:<b64>,2:<b64>".
	TokenEncKeys string
	// SIWEDomain is the dashboard host[:port] wallets sign for (wallet linking).
	SIWEDomain string
	// Local development only: cmd/local polls this queue (ElasticMQ) instead
	// of receiving SQS events from Lambda.
	LocalQueueURL string
}

// BitGo environments. Production is only allowed once the Go/No-Go gates pass.
const (
	BitGoTestURL = "https://app.bitgo-test.com"
	BitGoProdURL = "https://app.bitgo.com"
)

type secretFetcher func(ctx context.Context, name string) (string, error)

// Load reads configuration; secrets are resolved through SSM when the *_SSM
// variant of a variable is set.
func Load(ctx context.Context) (*Config, error) {
	return load(ctx, os.Getenv, ssmFetcher())
}

func load(ctx context.Context, getenv func(string) string, fetch secretFetcher) (*Config, error) {
	c := &Config{
		Env:          orDefault(getenv("VAULTOPS_ENV"), "local"),
		BitGoBaseURL: orDefault(getenv("BITGO_BASE_URL"), BitGoTestURL),
	}
	tok, err := secret(ctx, getenv, fetch, "BITGO_TOKEN")
	if err != nil {
		return nil, err
	}
	c.BitGoToken = tok
	if c.DatabaseURL, err = secret(ctx, getenv, fetch, "DATABASE_URL"); err != nil {
		return nil, err
	}
	if c.TokenEncKeys, err = secret(ctx, getenv, fetch, "TOKEN_ENC_KEYS"); err != nil {
		return nil, err
	}
	c.LocalQueueURL = getenv("LOCAL_QUEUE_URL")
	c.SIWEDomain = getenv("SIWE_DOMAIN")
	return c, c.validate()
}

func (c *Config) validate() error {
	c.BitGoBaseURL = strings.TrimRight(c.BitGoBaseURL, "/")
	if c.Env != "prod" && c.BitGoBaseURL == BitGoProdURL {
		return fmt.Errorf("config: BitGo production URL is only allowed when VAULTOPS_ENV=prod")
	}
	if c.BitGoToken != "" && c.Env != "local" {
		return fmt.Errorf("config: a global BITGO_TOKEN is only allowed locally; organizations use their own connections")
	}
	if c.LocalQueueURL != "" && c.Env != "local" {
		return fmt.Errorf("config: LOCAL_QUEUE_URL is only for VAULTOPS_ENV=local")
	}
	return nil
}

func secret(ctx context.Context, getenv func(string) string, fetch secretFetcher, key string) (string, error) {
	if name := getenv(key + "_SSM"); name != "" {
		v, err := fetch(ctx, name)
		if err != nil {
			return "", fmt.Errorf("config: read %s from SSM: %w", key, err)
		}
		return v, nil
	}
	return getenv(key), nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func ssmFetcher() secretFetcher {
	var (
		once   sync.Once
		client *ssm.Client
		cerr   error
	)
	return func(ctx context.Context, name string) (string, error) {
		once.Do(func() {
			cfg, err := awsconfig.LoadDefaultConfig(ctx)
			if err != nil {
				cerr = err
				return
			}
			client = ssm.NewFromConfig(cfg)
		})
		if cerr != nil {
			return "", cerr
		}
		decrypt := true
		out, err := client.GetParameter(ctx, &ssm.GetParameterInput{Name: &name, WithDecryption: &decrypt})
		if err != nil {
			return "", err
		}
		return *out.Parameter.Value, nil
	}
}
