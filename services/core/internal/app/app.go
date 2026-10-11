// Package app wires configuration and dependencies once per process, shared
// by every entry point (Lambdas and cmd/local).
package app

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/vaultops/core/internal/anchor"
	"github.com/vaultops/core/internal/bitgo"
	"github.com/vaultops/core/internal/config"
	"github.com/vaultops/core/internal/connections"
	"github.com/vaultops/core/internal/crypto"
	"github.com/vaultops/core/internal/httpapi"
	"github.com/vaultops/core/internal/identity"
	"github.com/vaultops/core/internal/ingest"
	"github.com/vaultops/core/internal/objects"
	"github.com/vaultops/core/internal/recon"
	"github.com/vaultops/core/internal/store"
)

type App struct {
	Config  *config.Config
	Log     *slog.Logger
	BitGo   *bitgo.Client
	Store   *store.Store // nil when DATABASE_URL is not configured
	HTTP    http.Handler
	Conns   *connections.Resolver // nil without a store
	Ingest  *ingest.Processor     // nil without a store
	Anchor  *anchor.Anchorer      // nil without a store and an object store
	Recon   *recon.Engine         // nil without a store
	Reports objects.Store         // nil unless R2 or REPORTS_DIR is configured
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
	if a.Store != nil {
		a.Recon = &recon.Engine{Store: a.Store, Log: log, Lookback: cfg.ReconLookback,
			BitGoFor: func(ctx context.Context, org uuid.UUID) (recon.BitGo, error) { return a.Conns.ForOrg(ctx, org) }}
		var anchors objects.Store
		switch {
		case cfg.R2Endpoint != "" && cfg.R2AuditBucket != "":
			anchors = objects.NewR2(cfg.R2Endpoint, cfg.R2AuditBucket, cfg.R2AccessKeyID, cfg.R2SecretAccessKey, true)
		case cfg.AnchorDir != "":
			anchors = objects.Dir{Root: cfg.AnchorDir, WriteOnce: true}
		}
		if anchors != nil {
			a.Anchor = &anchor.Anchorer{Store: a.Store, Objects: anchors, Log: log}
		}
		switch {
		case cfg.R2Endpoint != "" && cfg.R2ReportsBucket != "":
			a.Reports = objects.NewR2(cfg.R2Endpoint, cfg.R2ReportsBucket, cfg.R2AccessKeyID, cfg.R2SecretAccessKey, false)
		case cfg.ReportsDir != "":
			a.Reports = objects.Dir{Root: cfg.ReportsDir}
		}
	}
	var siwe *identity.SIWE
	if cfg.SIWEDomain != "" {
		siwe = &identity.SIWE{Domain: cfg.SIWEDomain}
	}
	a.HTTP = httpapi.New(httpapi.Deps{Log: log, Store: a.Store, Conns: a.Conns, SIWE: siwe, Recon: a.Recon, Reports: a.Reports})
	return a, nil
}
