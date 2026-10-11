// Command local serves the same handlers as the Lambdas over plain HTTP for
// development and acceptance tests. It is never deployed.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/vaultops/core/internal/app"
	"github.com/vaultops/core/internal/devserver"
	"github.com/vaultops/core/internal/localsqs"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	a, err := app.New(context.Background(), log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	addr := os.Getenv("CORE_ADDR")
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	if q := a.Config.LocalQueueURL; q != "" {
		if a.Ingest == nil {
			log.Error("LOCAL_QUEUE_URL needs DATABASE_URL")
			os.Exit(1)
		}
		go func() { _ = localsqs.Run(context.Background(), q, a.Ingest.HandleSQS, log) }()
	}
	log.Info("core listening", "addr", addr, "bitgo", a.Config.BitGoBaseURL, "db", a.Store != nil)
	if err := devserver.ListenAndServe(addr, a.HTTP); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
