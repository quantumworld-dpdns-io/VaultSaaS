# VaultOps threat model (STRIDE)

Scope: the MVP (M1–M5). VaultOps is **non-custodial and read-only toward BitGo**
(D5): it never holds keys or passphrases, and it never builds, signs or sends
transactions. The worst realistic outcome is therefore **disclosure or
falsification of treasury records**, not direct loss of funds. Phase 2
(approvals) changes that and must extend this document before it ships.

## Assets

| Asset | Where | Why it matters |
|---|---|---|
| Per-org BitGo access tokens | `app.bitgo_connections` (AES-256-GCM, AAD = org id) | Read access to a customer's wallets and history |
| Ledger, findings, expected payments | Supabase Postgres, schema `app` | Customer's financial records |
| Audit log | `app.audit_logs` (hash chain) + R2 anchors (bucket lock) | Evidence for customers and auditors |
| Edge IAM user key | Worker secret | Can invoke the core Lambda and enqueue SQS messages |
| Token keyring, DB URL, R2 keys | SSM SecureString (aws/ssm key) | Unlock the above |
| Webhook path tokens | Hash only in DB; plaintext shown once | Route webhooks to a tenant |

## Trust boundaries

Browser ⇄ **Cloudflare edge** (Supabase JWT) ⇄ **AWS core** (SigV4 / AWS_IAM) ⇄ **Supabase** (RLS) ⇄ **BitGo API** (per-org token).
BitGo ⇄ edge webhook ingress (unauthenticated except for the path token).

## STRIDE

| # | Threat | Category | Controls | Verified by |
|---|---|---|---|---|
| T1 | Forged or replayed BitGo webhook books a fake deposit | Spoofing / Tampering | Webhook body is only a hint: the worker re-fetches the transfer from BitGo with the org's own token before booking (P2); unknown transfers are rejected with an alert. Unguessable 256-bit per-tenant path token. Body-hash dedupe. Optional BitGo source-IP WAF rule. | `TestWebhookPayloadIsNotTrusted`, `TestForgedTransferIsRejectedWithAlert`, Robot M2 |
| T2 | Tenant A reads or changes tenant B's data (BOLA/IDOR) | Information disclosure / Elevation | Membership and role are re-checked on every request; identical 403 for foreign and non-existent orgs. Postgres RLS on every `app` table, keyed on `app.org_id`. Runtime role is neither superuser nor BYPASSRLS. Each org uses its own BitGo token. | `TestCrossTenantAccessIsDenied`, `TestCrossTenantRowsAreInvisible`, `TestEveryTenantTableHasRLS`, Robot M3 |
| T3 | Client asserts its own identity or role | Spoofing / Elevation | Edge verifies the Supabase JWT (JWKS, issuer, audience, role) and *replaces* identity headers; the core is reachable only via SigV4 (Function URL AWS_IAM). | edge `ignores identity headers supplied by the client`, Robot M3 |
| T4 | BitGo token theft | Information disclosure | Sealed with AES-256-GCM, bound to the org id (AAD); the keyring lives in SSM; tokens are never returned or logged. Read-only scopes are recommended in the UI. | `TestSealOpenAndBinding`, `TestBitGoConnectionIsPerOrgAndEncrypted` |
| T5 | Edge IAM key leak | Elevation | Only `lambda:InvokeFunction(Url)` on one function plus `sqs:SendMessage` on one queue. Never in Terraform state. 90-day rotation ([runbook](runbooks/rotate-edge-key.md)). The core still re-checks membership, so a leaked key without a Supabase identity can at most enqueue webhook hints (which are re-verified). | compliance D8 |
| T6 | Audit history rewritten by someone with DB owner rights | Tampering / Repudiation | Append-only triggers; per-org sha256 hash chain; daily anchors in an R2 bucket with a bucket lock (write-once). A fully recomputed chain is caught by the anchor check (critical alert). | `TestAuditLogIsAppendOnlyAndTamperEvident`, `TestAnchorDetectsRecomputedHistory`, Robot M3 |
| T7 | VaultOps used to move funds | Elevation | No send/sign/key endpoint can leave the process (client guard); no passphrases stored; reconciliation uses list endpoints only. | `TestGuardBlocksFundMovement`, compliance D5 |
| T8 | Wallet signature used to sign in or take over an account (D4) | Spoofing | No wallet sign-in/sign-up route; Supabase Web3 sign-in disabled; linking needs an authenticated user, a single-use 5-minute nonce, a matching domain and a fixed statement; one address belongs to one user. | `TestWalletCannotCreateAccount`, Robot M3 D4 |
| T9 | CSV formula injection via references/addresses in reports | Tampering | Report cells starting with `= + - @ TAB CR` are prefixed with `'` (integers kept). Import accepts integer base units only. | `TestSafeCell`, Robot M4 |
| T10 | Cost-exhaustion DoS against free tiers | Denial of service | Zone rate limit; Worker body limits; SQS consumer capped at 2 concurrent Lambdas; zero-spend and $5 AWS budgets; logs kept for 7 days; no paid AWS services can be created (policy gate). | `free_only.rego`, compliance P0 |
| T11 | Malicious dependency or action | Tampering | Lockfiles; actions pinned by SHA; Dependabot; govulncheck, npm audit, gosec, semgrep, trivy in CI; install scripts approved explicitly. | `ci.yml` |
| T12 | Secrets in source or state | Information disclosure | SSM SecureString shells (values set out-of-band); wrangler secrets; `.env`/`*.local` ignored; semgrep secrets ruleset. | compliance P5 |
| T13 | Webhook processing silently stops | Denial of service | DLQ after 5 receives with an alert on the last; DLQ CloudWatch alarm → SNS email; `dlq_backlog` rule; scheduled reconciliation backfills missed webhooks. | Robot M2, M4, M5 |
| T14 | Lost database (Supabase free tier has no backups) | Availability | Daily encrypted `pg_dump` to R2 and a monthly restore drill ([runbook](runbooks/backup-restore.md)). | `db-backup.yml` |

## Residual risks / open items

- BitGo webhook signatures are not verified yet (Week 0: confirm BitGo's mechanism). T1 relies on re-fetching and on the path token meanwhile.
- `token_expires_at` is entered by an admin. Read it from BitGo once the API is confirmed in Week 0.
- Phase 2 approvals will introduce a write path to BitGo. That needs EIP-712 step-up, per-approver tokens, replay protection (nonce + expiry) and an update to this model **before** release.
- Supabase free tier has no SLA. Upgrade before charging customers (Go/No-Go gate).
