# VaultOps

Secure Crypto Treasury & Stablecoin Reconciliation SaaS (B2B, non-custodial).
The plan lives in [`.claude/implementation_plan.md`](.claude/implementation_plan.md).

```
apps/web        React + Vite dashboard (Cloudflare Pages)
apps/edge       Cloudflare Worker: auth, routing, webhook ingress
services/core   Go Lambdas: api / worker / recon — the only writer of ledger state
db/migrations   Postgres (Supabase) migrations
infra           Terraform for Cloudflare + AWS (Always Free services only)
test            Robot Framework acceptance suites
```

## Local development

Requirements: Go ≥ 1.23, Node ≥ 20, Docker, Terraform ≥ 1.10, Python ≥ 3.11.

```bash
make deps        # npm install + go mod download
make local-up    # docker compose: postgres, sqs (elasticmq), r2 (minio)
make mock-bitgo  # fake BitGo API on :8090
make core-local  # Go api on :8080 (same handler as the Lambda)
make edge-dev    # Worker on :8787
make web-dev     # dashboard on :5173
make test        # go test + worker tests + terraform validate + policy
make robot       # Robot Framework acceptance suites (test/)
```

Nothing in this repo deploys automatically from a laptop. Deployment runs from
GitHub Actions (see `.github/workflows`).
