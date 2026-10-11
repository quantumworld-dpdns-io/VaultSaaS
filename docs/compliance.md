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

## M3 — Tenancy, Auth, RBAC, Audit (2026-10-11)

Result: **M3 suite 12/12, M1 11/11, M2 10/10, compliance (m1–m3) 18/18 — 51/51.**

Acceptance evidence:
- **BOLA/IDOR** — through the edge, Alice gets the same `403 forbidden` for Bob's org and for a random org id on wallets, transfers, alerts, audit, members, invitations and bitgo-connection. Client-supplied `X-Vaultops-*` headers are dropped by the edge. At the DB layer (Go tests), RLS hides and refuses foreign rows.
- **Audit immutability** — `UPDATE`/`DELETE`/`TRUNCATE` on `app.audit_logs` are rejected by trigger, even as the owner. A rewrite that recomputes the whole chain still verifies internally, but the daily R2 anchor flags it (`audit_anchor_mismatch`, critical).

Manual review:
- **P0 / D2** — only additions are an EventBridge schedule and two SSM Standard SecureString shells (R2 credentials). Policy passes on both environments. Audit anchors use Cloudflare R2, not S3.
- **P1** — ledger writes still only via the Go domain service; the new API writes membership/connection/audit rows through `store.InOrgAs`.
- **P3** — RLS on every `app` table (`TestEveryTenantTableHasRLS`). The runtime role is neither superuser nor BYPASSRLS. Cross-tenant entry points are narrow SECURITY DEFINER functions.
- **P4** — `supabase/config.toml` exposes `public` only. `anon`/`authenticated` have no USAGE on `app`. Member emails come through `app.current_org_members()`, not a grant on `auth.users`.
- **P5** — the browser holds only the Supabase anon key (public by design) and the user's access token. BitGo tokens are AES-256-GCM sealed and bound to the org id.
- **D3** — Supabase Auth JWTs (JWKS, ES256/RS256); locally `cmd/mockauth` issues Supabase-shaped tokens.
- **D4** — wallets are linked identities only: no wallet sign-in/sign-up route, Supabase Web3 sign-in disabled, linking requires a signed-in user plus a single-use nonce, and one address maps to one user.
  - *Deviation:* the plan said "Supabase Web3 identity linking"; VaultOps verifies EIP-4361 itself and keeps Supabase Web3 off, which enforces D4 more strictly.
- **D5** — no new BitGo write paths. Connecting BitGo is verified with a read-only `ListWallets`.
- **Tenant isolation fix** — the shared `BITGO_TOKEN` is now refused outside `local`, so each org must use its own connection. Without this, all tenants would have seen the same wallets.
- **Bug found by the M3 suite:** the local anchor store silently skipped earlier anchors when its root path contained `..` (fixed in `0102e11`; regression test added). R2 itself is unaffected (key listing).

## M4 — Reconciliation & Reporting (2026-10-11)

Result: **M4 suite 9/9; full regression M1 11, M2 10, M3 12, compliance (m1–m4) 21/21 — 63/63.**

Acceptance evidence (one run against mock BitGo):

| Scenario | Classified as |
|---|---|
| Transfer at BitGo, webhook never arrived (A, B, X) | `missed_webhook` ×3, backfilled into the ledger |
| Expected 1000 at addr_a, received 1000 | `matched` (INV-A) |
| Expected 2000 at addr_b, received 1999 | `amount_mismatch` (INV-B → `mismatched`) |
| Received 777 at addr_x, nothing expected (extra inbound) | `missing_internal` |
| Ledger pending, BitGo confirmed (confirmation webhook lost) | `status_mismatch`, corrected to `confirmed`; then `matched` (INV-D) |
| Expected payment overdue, nothing received | `missing_bitgo` (stays `open`) |

A second (scheduled) run repeats nothing. The daily report contains the day's transfers and findings, with the formula-like reference written as `'=SUM(1+1)`.

Manual review:
- **P0 / D2** — new infra is env vars only (reports bucket name). Reports go to Cloudflare R2 via presigned URLs, not S3. Policy passes on both environments.
- **P1** — reconciliation books and corrects transfers only through `domain.ApplyObservation`; no direct writes to `app.transfers` outside the store.
- **P2** — recon reads BitGo's own transfer list (authoritative), never webhook bodies.
- **P3** — new tables (`expected_payments`, `reconciliation_runs/items`) have `organization_id` + RLS (covered by `TestEveryTenantTableHasRLS`). A foreign run id returns an empty item list.
- **P5** — report cells starting with `= + - @` are neutralised (integers excepted). Import accepts integer base units only.
- **D5** — the recon engine's BitGo interface is `ListWallets` + `ListTransfers` only.
- **D6** — report day boundaries use Asia/Taipei.
- **Plan extension:** a sixth finding kind, `missed_webhook`, separates "ledger gap" from the plan's `missing_internal` (unexpected inbound). Recorded in the plan.
- **Test-environment note:** headless Chrome could not read upload files under `~/Documents` (macOS privacy controls), so the UI test writes its CSV to the system temp dir.
