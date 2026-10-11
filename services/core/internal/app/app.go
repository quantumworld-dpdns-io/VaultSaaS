// Package app wires configuration and dependencies once per process, shared
// by every entry point (Lambdas and cmd/local).
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/config"
	"github.com/vaultops/core/internal/httpapi"
	"github.com/vaultops/core/internal/ingest"
	"github.com/vaultops/core/internal/store"
)

type App struct {
	Config *config.Config
	Log    *slog.Logger
	BitGo  *bitgo.Client
	Store  *store.Store // nil when DATABASE_URL is not configured
	HTTP   http.Handler
	Ingest *ingest.Processor // nil without a store
}

func New(ctx context.Context, log *slog.Logger) (*App, error) {
	cfg, err := config.Load(ctx)
	if err != nil {
		return nil, err
	}
	bg := bitgo.New(cfg.BitGoBaseURL, cfg.BitGoToken)
	a := &App{Config: cfg, Log: log, BitGo: bg}
	if cfg.DatabaseURL != "" {
		if a.Store, err = store.Open(ctx, cfg.DatabaseURL); err != nil {
			return nil, err
		}
		a.Ingest = &ingest.Processor{Store: ingest.PG{S: a.Store}, BitGo: bg, Log: log}
	}
	a.HTTP = httpapi.New(httpapi.Deps{Log: log, Wallets: bg})
	return a, nil
}
