// Package app wires configuration and dependencies once per process, shared
// by every entry point (Lambdas and cmd/local).
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/vaultops/core/internal/config"
	"github.com/vaultops/core/internal/httpapi"
)

type App struct {
	Config *config.Config
	HTTP   http.Handler
}

func New(ctx context.Context, log *slog.Logger) (*App, error) {
	cfg, err := config.Load(ctx)
	if err != nil {
		return nil, err
	}
	return &App{
		Config: cfg,
		HTTP:   httpapi.New(httpapi.Deps{Log: log}),
	}, nil
}
