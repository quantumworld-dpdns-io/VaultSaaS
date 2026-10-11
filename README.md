# VaultOps

**Secure crypto treasury & stablecoin reconciliation for B2B teams — non-custodial, read-only toward BitGo.**

VaultOps watches an organization's BitGo wallets. It books every transfer only after re-verifying it with BitGo, reconciles transfers against the payments the customer expects, and raises security alerts. All of this happens inside a multi-tenant SaaS with row-level isolation and a tamper-evident audit log. It never holds keys and never moves funds.

- Plan & decisions: [`.claude/implementation_plan.md`](.claude/implementation_plan.md)
- Compliance log per milestone: [`docs/compliance.md`](docs/compliance.md)
- Threat model: [`docs/threat-model.md`](docs/threat-model.md) · Runbooks: [`docs/runbooks/`](docs/runbooks)

## Architecture

```
Browser ──► Cloudflare Pages (React)            Supabase Auth (email link)
   │                                                   │ JWT (JWKS)
   └──────► Cloudflare Worker (edge) ◄─────────────────┘
              │ verify JWT, WAF/rate limit, webhook ingress
              │ SigV4 (IAM user: invoke + sendMessage only)
              ▼
         AWS (ap-northeast-1, Always Free only)
           Lambda api  ─┐
           SQS + DLQ ──► Lambda worker ─┐        BitGo API (per-org token,
           Scheduler ──► Lambda recon  ─┼──────►  read-only; testnet until
                                        ▼          the Go/No-Go gates pass)
                              Supabase Postgres (RLS on every table)
                              Cloudflare R2: reports, audit anchors (bucket lock), backups
```

| Path | What |
|---|---|
| `apps/web` | React + Vite + TanStack Query dashboard |
| `apps/edge` | Cloudflare Worker (Hono): JWT, routing, webhook → SQS |
| `services/core` | Go: `api`/`worker`/`recon` Lambdas, `vaultctl` ops CLI, local mocks |
| `db/migrations` | Postgres migrations (goose) |
| `infra` | Terraform for AWS + Cloudflare, plus the principle-0 policy (`infra/policy`) |
| `test` | Robot Framework acceptance + compliance suites |

## Quick start (local, no cloud accounts needed)

Requirements: Go ≥ 1.27, Node ≥ 20, Docker (for Postgres), Java ≥ 17 (ElasticMQ), Terraform ≥ 1.10, Python ≥ 3.11, Chrome (UI tests).

```bash
make tools deps          # conftest, ElasticMQ jar, Go + npm dependencies
make test                # unit tests, Terraform validate, principle-0 policy
make robot               # every Robot suite (M1–M5 + compliance) against a full local stack
make demo                # just the end-to-end demo suite
```

To click around yourself, run each of these in its own terminal:

```bash
make local-up migrate-local         # Postgres (Supabase stand-in) + schema
make sqs-local                      # ElasticMQ (SQS stand-in)
make mock-bitgo                     # fake BitGo on :8090 (token: local-dev-token)
make mock-auth                      # fake Supabase Auth on :8095
make core-local                     # Go core on :8080
make edge-dev                       # Worker on :8787
cp apps/web/.env.example apps/web/.env.local && make web-dev   # dashboard on :5173
```

Sign in with any email. Create an organization, then connect BitGo with the token `local-dev-token`.

## Demo storyline (`make demo`)

`test/suites/m5_end_to_end.robot` drives the full path and saves screenshots to `test/results/`:

1. An admin signs in, creates an organization, connects a read-only BitGo token (with its expiry date) and sets a large-transfer threshold.
2. A **testnet deposit** happens at BitGo. Its **webhook** reaches the edge, is queued in SQS, and the worker **re-fetches it from BitGo** before **booking** it.
3. A **large send to a never-seen address** raises `large_transfer` + `new_destination` alerts.
4. The **wallet policy is changed** at BitGo, which raises `wallet_policy_changed`.
5. The customer **imports expected payments**. A deposit whose webhook was lost is **backfilled by reconciliation** and matched; an under-payment is flagged as `amount_mismatch`.
6. A BitGo outage pushes webhooks into the **dead-letter queue**. The scheduled job raises `dlq_backlog` and `bitgo_token_expiring`.
7. The **daily report** (CSV, formula-safe) contains the day's transfers and findings, and the **audit chain verifies**.

## Principles enforced by code and tests

- **AWS uses Always Free services only.** Every Terraform plan is checked by `infra/policy/free_only.rego`.
- **Single writer.** Only the Go domain service changes ledger state.
- **Webhooks are hints.** Every booked fact is re-read from BitGo.
- **Tenant isolation.** Postgres RLS plus per-request membership checks.
- **No tenant data through the Supabase Data API, and no service keys in the browser.**
- **Read-only toward BitGo.** No send/sign/key call can leave the process. Wallets are linked identities only, never a sign-in method.

## Status

| Milestone | State |
|---|---|
| M1 Foundation · M2 Ingest · M3 Tenancy/Auth/Audit · M4 Reconciliation | ✅ done, Robot-verified |
| M5 Security alerts & demo | 🚧 in progress |
| Week 0 (BitGo account, cloud accounts, `terraform apply`) | ⏳ needs real credentials |
| Phase 2 (approvals, billing) | ⏳ gated by Go/No-Go |

Nothing in this repo deploys from a laptop. Deployments run from GitHub Actions (`.github/workflows`).
