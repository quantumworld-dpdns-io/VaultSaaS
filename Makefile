SHELL := /bin/bash
ROOT  := $(shell pwd)
CORE  := services/core
TOOLS := $(ROOT)/.tools/bin
export PATH := $(TOOLS):$(PATH)
LAMBDAS := $(filter api worker recon,$(notdir $(wildcard $(CORE)/cmd/*)))

.PHONY: tools deps local-up local-down sqs-local migrate-local mock-bitgo core-local edge-dev web-dev \
        build-core test test-core test-edge test-web tf-validate policy robot

ELASTICMQ_JAR := $(ROOT)/.tools/elasticmq-server-1.7.1.jar

tools:
	mkdir -p $(TOOLS)
	GOBIN=$(TOOLS) go install github.com/open-policy-agent/conftest@v0.71.1
	test -f $(ELASTICMQ_JAR) || curl -fsSL -o $(ELASTICMQ_JAR) \
	  https://s3-eu-west-1.amazonaws.com/softwaremill-public/elasticmq-server-1.7.1.jar

deps: tools
	cd $(CORE) && go mod download
	cd apps/edge && npm install
	cd apps/web && npm install

local-up:
	docker compose -f docker-compose.local.yml up -d --wait postgres

# SQS stand-in without Docker (the compose service works too where image pulls do).
sqs-local:
	java -Dconfig.file=db/local/elasticmq.conf -jar $(ELASTICMQ_JAR)

LOCAL_OWNER_DB := postgres://postgres:postgres@127.0.0.1:54322/vaultops?sslmode=disable
migrate-local:
	cd $(CORE) && DATABASE_OWNER_URL="$(LOCAL_OWNER_DB)" VAULTOPS_APP_DB_PASSWORD=local-app-password-123 \
	  go run ./cmd/vaultctl migrate --dir ../../db/migrations --set-app-password

local-down:
	docker compose -f docker-compose.local.yml down -v

mock-bitgo:
	cd $(CORE) && go run ./cmd/mockbitgo

core-local:
	cd $(CORE) && go run ./cmd/local

edge-dev:
	cd apps/edge && npx wrangler dev --local --port 8787

web-dev:
	cd apps/web && npm run dev

# Lambda artifacts: linux/arm64, binary must be named "bootstrap" (provided.al2023).
build-core:
	@for fn in $(LAMBDAS); do \
	  mkdir -p $(CORE)/bin/$$fn; \
	  (cd $(CORE) && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" \
	    -tags lambda.norpc -o bin/$$fn/bootstrap ./cmd/$$fn) || exit 1; \
	  (cd $(CORE)/bin/$$fn && zip -q -j ../$$fn.zip bootstrap); \
	done

test: test-core test-edge test-web tf-validate policy

# DB-backed tests run when VAULTOPS_TEST_DATABASE_URL is set (make local-up), else skip.
test-core:
	cd $(CORE) && go vet ./... && go test ./...

test-edge:
	cd apps/edge && npm test

test-web:
	cd apps/web && npm test && npm run build

tf-validate:
	./infra/scripts/validate.sh

policy:
	./infra/scripts/policy-check.sh

robot:
	./test/run.sh
