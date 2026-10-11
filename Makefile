SHELL := /bin/bash
ROOT  := $(shell pwd)
CORE  := services/core
TOOLS := $(ROOT)/.tools/bin
export PATH := $(TOOLS):$(PATH)
LAMBDAS := $(filter api worker recon,$(notdir $(wildcard $(CORE)/cmd/*)))

.PHONY: deps local-up local-down mock-bitgo core-local edge-dev web-dev \
        build-core test test-core test-edge test-web tf-validate policy robot

deps:
	cd $(CORE) && go mod download
	cd apps/edge && npm install
	cd apps/web && npm install

local-up:
	docker compose -f docker-compose.local.yml up -d --wait

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

test-core:
	cd $(CORE) && go vet ./... && go test ./...

test-edge:
	cd apps/edge && npm test

test-web:
	cd apps/web && npm run build

tf-validate:
	./infra/scripts/validate.sh

policy:
	./infra/scripts/policy-check.sh

robot:
	./test/run.sh
