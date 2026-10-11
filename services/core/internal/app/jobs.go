package app

// Scheduled jobs shared by the recon Lambda and vaultctl.

import (
	"context"
	"fmt"
	"time"

	"github.com/vaultops/core/internal/report"
	"github.com/vaultops/core/internal/rules"
	"github.com/vaultops/core/internal/store"
)

// ReconcileAll runs every connected organization; one failing tenant does not
// stop the others, but the invocation errors so the CloudWatch alarm fires.
func (a *App) ReconcileAll(ctx context.Context) (map[string]any, error) {
	log := a.Log
	if a.Recon == nil {
		return nil, fmt.Errorf("reconcile needs DATABASE_URL")
	}
	orgs, err := a.Store.OrganizationsWithBitGo(ctx)
	if err != nil {
		return nil, err
	}
	failed := 0
	for _, org := range orgs {
		sum, err := a.Recon.Run(ctx, org, "schedule")
		if err != nil {
			failed++
			log.Error("reconciliation failed", "org", org, "run", sum.RunID, "err", err)
			continue
		}
		log.Info("reconciled", "org", org, "run", sum.RunID, "stats", sum.Stats)
	}
	// Time-based alert rules for every tenant (token expiry, DLQ backlog).
	all, err := a.Store.AllOrganizations(ctx)
	if err != nil {
		return nil, err
	}
	for _, org := range all {
		if err := a.Store.InOrg(ctx, org, func(tx *store.Tx) error { return rules.Scheduled(tx, org, time.Now()) }); err != nil {
			failed++
			log.Error("scheduled rules failed", "org", org, "err", err)
		}
	}
	out := map[string]any{"organizations": len(orgs), "failed": failed}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d organizations failed to reconcile", failed, len(orgs))
	}
	return out, nil
}

// PublishDailyReports publishes yesterday's (Asia/Taipei) report for every org.
func (a *App) PublishDailyReports(ctx context.Context) (map[string]any, error) {
	log := a.Log
	if a.Store == nil || a.Reports == nil {
		return nil, fmt.Errorf("daily_report needs DATABASE_URL and R2 reports settings")
	}
	orgs, err := a.Store.AllOrganizations(ctx)
	if err != nil {
		return nil, err
	}
	day := time.Now().In(report.Taipei).Add(-24 * time.Hour)
	failed := 0
	for _, org := range orgs {
		key, _, err := report.Publish(ctx, a.Store, a.Reports, org, day)
		if err != nil {
			failed++
			log.Error("daily report failed", "org", org, "err", err)
			continue
		}
		log.Info("daily report published", "org", org, "key", key)
	}
	if failed > 0 {
		return nil, fmt.Errorf("%d of %d daily reports failed", failed, len(orgs))
	}
	return map[string]any{"published": len(orgs), "day": day.Format("2006-01-02")}, nil
}
