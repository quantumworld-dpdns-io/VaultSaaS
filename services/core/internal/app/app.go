// Package app wires configuration and dependencies once per process, shared
// by every entry point (Lambdas and cmd/local).
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/config"
	"github.com/vaultops/core/internal/connections"
	"github.com/vaultops/core/internal/crypto"
	"github.com/vaultops/core/internal/httpapi"
	"github.com/vaultops/core/internal/identity"
	"github.com/vaultops/core/internal/ingest"
	"github.com/vaultops/core/internal/store"
)

type App struct {
	Config *config.Config
	Log    *slog.Logger
	BitGo  *bitgo.Client
	Store  *store.Store // nil when DATABASE_URL is not configured
	HTTP   http.Handler
	Conns  *connections.Resolver // nil without a store
	Ingest *ingest.Processor     // nil without a store
}

func New(ctx context.Context, log *slog.Logger) (*App, error) {
	cfg, err := config.Load(ctx)
	if err != nil {
		return nil, err
	}
	a := &App{Config: cfg, Log: log}
	if cfg.BitGoToken != "" { // local only (enforced by config)
		a.BitGo = bitgo.New(cfg.BitGoBaseURL, cfg.BitGoToken)
	}
	var kr *crypto.Keyring
	if cfg.TokenEncKeys != "" {
		if kr, err = crypto.ParseKeyring(cfg.TokenEncKeys); err != nil {
			return nil, err
		}
	}
	if cfg.DatabaseURL != "" {
		if a.Store, err = store.Open(ctx, cfg.DatabaseURL); err != nil {
			return nil, err
		}
		a.Conns = &connections.Resolver{Store: a.Store, Keyring: kr, BaseURL: cfg.BitGoBaseURL, LocalFallback: a.BitGo}
		a.Ingest = &ingest.Processor{Store: ingest.PG{S: a.Store}, Log: log,
			BitGoFor: func(ctx context.Context, org uuid.UUID) (ingest.BitGo, error) { return a.Conns.ForOrg(ctx, org) }}
	}
	var siwe *identity.SIWE
	if cfg.SIWEDomain != "" {
		siwe = &identity.SIWE{Domain: cfg.SIWEDomain}
	}
	a.HTTP = httpapi.New(httpapi.Deps{Log: log, Store: a.Store, Conns: a.Conns, SIWE: siwe})
	return a, nil
}
