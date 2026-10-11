# Compliance log — Decision Log (D1–D9) & core principles (0–5)

Each milestone ends with `test/run.sh --include <milestone>`, which runs the
acceptance suite and `test/suites/compliance.robot`. This file records the
outcome and any judgement calls the automated checks cannot make.

| Item | How it is checked |
|---|---|
| P0 / D2 — AWS free services only | `infra/policy/free_only.rego` on offline plans of dev + prod; grep for forbidden resource types, VPC/X-Ray/provisioned concurrency |
| P1 — single ledger writer | edge has no DB driver; no SQL writes outside `services/core` |
| P2 — webhooks are hints | worker re-fetches transfers (`TestWebhookPayloadIsNotTrusted`) |
| P3 — tenant isolation | every tenant table has `organization_id` + RLS (`TestEveryTenantTableHasRLS`) |
| P4 — Data API not exposed | `supabase/config.toml` exposes only `public` |
| P5 — no service keys in the browser | grep `apps/web/src` and the built bundle |
| D3 — Supabase | no other datastore; pgx driver |
| D4 — wallet = linked identity | `TestWalletCannotCreateAccount` |
| D5 — read + approve, never move funds | BitGo client guard tests; no send/sign/passphrase code |
| D6 — Tokyo / APAC | every provider region `ap-northeast-1`; R2 `apac` |
| D8 — Cloudflare edge + Go Lambda | Function URL `AWS_IAM`; edge IAM user actions |
| D9 — testnet until gate 2 | config refuses BitGo prod URL outside prod; no prod URL in code/infra |
| D1, D7 | product/schedule decisions — reviewed by hand below |

## M1 — Foundation (2026-10-11)

Result: **M1 suite 11/11 pass, compliance (m1-tagged) 12/12 pass.**

Manual review:
- **D1** — no billing code yet; nothing charges customers before the Go/No-Go gates.
- **D7** — scope matches the M1 row of the plan (skeleton, Terraform, OIDC, api Lambda, BitGo client, dashboard).
- **D9** — no BitGo account yet, so every test uses `bitgomock`. The real testnet smoke test is still open (Week 0).
- **Plan deviations (both still principle-0 compliant), synced back into the plan:**
  - The edge IAM user also has `lambda:InvokeFunction` on the same function (needed for Function URLs since late 2025).
  - SNS topic + email subscription (Always Free: 1,000 emails/month) added so CloudWatch alarms can notify someone; alarms are capped at 5 per environment so dev + prod stay within the free 10.
- **Not verified here:** `terraform apply` against real AWS/Cloudflare accounts, and R2 support for `use_lockfile`. Both need credentials (Week 0).

## M2 — Ingest pipeline (2026-10-11)

Result: **M2 suite 10/10, M1 regression 11/11, compliance (m1+m2) 15/15 — 36/36.**

Acceptance evidence:
- One webhook replayed 10 times via `vaultctl replay` → `webhook_events.attempts = 10`, one `transfers` row, one `transfer.booked` audit entry.
- BitGo lookups forced to fail → the message reaches the DLQ after 5 receives; the event is `failed:5`, nothing is booked, and a `webhook_dead_lettered` security alert exists.

Manual review:
- **P1** — the edge only validates and enqueues; every ledger write goes through `domain.ApplyObservation` inside `store.InOrg`.
- **P2** — the worker books state/amount only from `GET …/transfer/{id}`. A body that claims `confirmed` with a larger amount is booked as BitGo's `pending`/`100`. Forged transfer IDs are rejected with an alert.
- **P3** — every tenant table has `organization_id NOT NULL`, and all access sets `app.org_id`. RLS policies themselves land in M3, as the plan schedules.
- **P4** — tenant tables live in schema `app`; goose's version table lives in the private `vaultops_meta` schema instead of `public`.
- **D3** — Postgres only; pgx in exec mode, compatible with the Supavisor transaction pooler.
- **D5** — the worker uses GET endpoints only (D5 guard tests unchanged).
- **Deviations:**
  - hand-written pgx SQL instead of sqlc (plan updated);
  - ElasticMQ runs from its jar locally because the Docker Desktop proxy blocks image pulls. The compose service is kept for machines where pulls work.
- **Open:** BitGo webhook signature verification is a TODO until Week 0 confirms BitGo's mechanism. Meanwhile, the unguessable per-tenant token plus mandatory re-fetch is the control.
