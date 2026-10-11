// Command api is the Lambda behind the Function URL.
package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/vaultops/core/internal/app"
	"github.com/vaultops/core/internal/lambdaurl"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	a, err := app.New(context.Background(), log)
	if err != nil {
		log.Error("startup failed", "err", err)
		os.Exit(1)
	}
	lambda.Start(lambdaurl.Handler(a.HTTP))
}
