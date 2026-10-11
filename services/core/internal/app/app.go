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
)

type App struct {
	Config *config.Config
	BitGo  *bitgo.Client
	HTTP   http.Handler
}

func New(ctx context.Context, log *slog.Logger) (*App, error) {
	cfg, err := config.Load(ctx)
	if err != nil {
		return nil, err
	}
	bg := bitgo.New(cfg.BitGoBaseURL, cfg.BitGoToken)
	return &App{
		Config: cfg,
		BitGo:  bg,
		HTTP:   httpapi.New(httpapi.Deps{Log: log, Wallets: bg}),
	}, nil
}
