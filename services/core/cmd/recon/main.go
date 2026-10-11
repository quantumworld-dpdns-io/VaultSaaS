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
		return a.ReconcileAll(ctx)
	case "daily_report":
		return a.PublishDailyReports(ctx)
	}
	return nil, fmt.Errorf("unknown task %q", task)
}
