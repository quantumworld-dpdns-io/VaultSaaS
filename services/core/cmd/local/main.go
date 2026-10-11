// Command local serves the same handlers as the Lambdas over plain HTTP for
// development and acceptance tests. It is never deployed.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/vaultops/core/internal/app"
	"github.com/vaultops/core/internal/devserver"
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
	log.Info("core listening", "addr", addr, "bitgo", a.Config.BitGoBaseURL)
	if err := devserver.ListenAndServe(addr, a.HTTP); err != nil {
		log.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
