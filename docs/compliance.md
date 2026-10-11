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
