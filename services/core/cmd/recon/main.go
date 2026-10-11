// Command recon is the scheduled Lambda (EventBridge Scheduler). The event
// names the task: {"task": "audit_anchor" | "reconcile" | "daily_report"}.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/vaultops/core/internal/app"
)

type event struct {
	Task string `json:"task"`
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a, err := app.New(context.Background(), log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	lambda.Start(func(ctx context.Context, ev event) (map[string]any, error) {
		return run(ctx, a, log, ev.Task)
	})
}

func run(ctx context.Context, a *app.App, log *slog.Logger, task string) (map[string]any, error) {
	switch task {
	case "audit_anchor":
		if a.Anchor == nil {
			return nil, fmt.Errorf("audit_anchor needs DATABASE_URL and R2 settings")
		}
		res, err := a.Anchor.Run(ctx)
		if err != nil {
			return nil, err
		}
		log.Info("audit anchors written", "anchored", res.Anchored, "problems", len(res.Problems))
		return map[string]any{"anchored": res.Anchored, "problems": res.Problems}, nil
	case "reconcile":
		return reconcileAll(ctx, a, log)
	case "daily_report":
		log.Info("task not implemented yet", "task", task)
		return map[string]any{"skipped": task}, nil
	}
	return nil, fmt.Errorf("unknown task %q", task)
}

// reconcileAll runs every connected organization; one failing tenant does not
// stop the others, but the invocation errors so the CloudWatch alarm fires.
func reconcileAll(ctx context.Context, a *app.App, log *slog.Logger) (map[string]any, error) {
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
	out := map[string]any{"organizations": len(orgs), "failed": failed}
	if failed > 0 {
		return out, fmt.Errorf("%d of %d organizations failed to reconcile", failed, len(orgs))
	}
	return out, nil
}
