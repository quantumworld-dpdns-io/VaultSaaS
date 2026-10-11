// Command worker is the SQS-triggered Lambda that applies webhook events.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/vaultops/core/internal/app"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a, err := app.New(context.Background(), log)
	if err == nil && a.Ingest == nil {
		log.Error("worker requires DATABASE_URL / DATABASE_URL_SSM")
		os.Exit(1)
	}
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	lambda.Start(a.Ingest.HandleSQS)
}
