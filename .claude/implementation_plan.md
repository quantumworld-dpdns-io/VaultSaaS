# VaultOps — Implementation Plan

> 來源：`.ignore.md`（BitGo × 低成本 Serverless 研究筆記）＋ 2026-10-11 需求拷問（grill）結論。
> 產品定位：**Secure Crypto Treasury & Stablecoin Reconciliation SaaS**（B2B、非託管）。

---

## 0. 已鎖定的決策（Decision Log）

| # | 議題 | 決定 | 理由／後果 |
|---|---|---|---|
| D1 | 產品目的 | **作品集 + 真實 Micro-SaaS 並行** | 工程主線與「客戶探索」副線同時推進；收費功能以 Go/No-Go 閘門控管 |
| D2 | AWS 使用原則 | **必須使用 AWS，但只用 Always Free 服務**；非永久免費者改用替代品 | 見 §2 服務對照表 |
| D3 | 資料庫 | **Supabase（Postgres + Auth）** | 本系統資料**持續進出**（24/7 webhook 寫入、排程對帳輪詢、Dashboard 讀取、報表匯出）→ 依你的規則選 Supabase；持續流量也避免免費專案 7 天閒置被暫停 |
| D4 | 登入 | Email / OAuth 為帳號根；**EVM 錢包（SIWE）為「連結身分」**，不可單獨建立帳號；組織成員資格只能經邀請 | 錢包只證明地址控制權，不證明公司身分 |
| D5 | 權限範圍 | **Read + Approve**：可檢視並核准／拒絕 BitGo pending approvals；**永不發起轉帳、永不持有私鑰／wallet passphrase** | Approve 列為 Phase 2，MVP 先完成唯讀 |
| D6 | 目標市場 | **台灣／APAC 團隊** | 區域：AWS `ap-northeast-1`、Supabase Tokyo；台灣個資法（PDPA）、洗錢防制法／VASP 規範需法律確認 |
| D7 | 投入時間 | **~10 小時／週** | 原 4 週路線拉長為 **Week 0 + 約 12 週 MVP**，Phase 2 另計 |
| D8 | 拓樸 | **Plan B 變體：Cloudflare Edge + AWS Go Lambda + Supabase** | Go 單一 Domain Service 掌握所有帳務狀態轉移 |
| D9 | BitGo | **尚無帳號** → Week 0 申請 testnet，並向 BitGo 詢問第三方 SaaS 商用授權與報價 | 商用條款未知前不得假設正毛利 |

---

## 1. 系統架構

```
                 ┌──────────────── Cloudflare ────────────────┐
 Browser ──────► │ Pages (React+Vite)                          │
   │  Supabase   │ Worker `edge`：JWT 驗證(Supabase JWKS)、     │
   │  Auth JS    │   WAF / Rate Limit、路由、Webhook 入口        │
   ▼             │ R2：報表、稽核匯出 (Bucket Lock)、TF state    │
 Supabase Auth   └───────┬───────────────────────┬─────────────┘
 (email/OAuth+SIWE)      │ SigV4 (aws4fetch)     │ SigV4 sqs:SendMessage
                         ▼                       ▼
            ┌────────── AWS (ap-northeast-1, Always Free only) ──────────┐
            │ Lambda `api` (Go, Function URL, AWS_IAM auth)               │
            │ SQS `webhook-events` + DLQ ──► Lambda `worker` (Go)         │
            │ EventBridge Scheduler ──► Lambda `recon` (Go, cron)         │
            │ SSM Parameter Store (SecureString, aws/ssm key)             │
            │ CloudWatch Logs (7-day retention) / Alarms                  │
            └───────────────┬───────────────────────────┬─────────────────┘
                            │ Supavisor pooler :6543    │ HTTPS
                            ▼                           ▼
                  Supabase Postgres (Tokyo)        BitGo API (testnet → prod)
```

**核心原則**
0. ** aws不可以使用任何付費服務
1. **單一寫入者**：只有 Go 服務（`api` / `worker` / `recon` 共用 `internal/domain`）能改變帳務狀態。Worker 只做驗證、路由、入列。
2. **Webhook 不可信**：Webhook 只是「提示」；worker 一律以 BitGo API 重新查詢 transfer / approval 實際狀態後才入帳（BitGo 官方建議）。
3. **租戶隔離雙層**：Go 服務層 org 授權檢查 + Postgres RLS（每個 transaction `SET LOCAL app.org_id`）。
4. **Supabase Data API 不暴露租戶資料**：租戶表放在 `app` schema，不加入 exposed schemas；前端只用 Supabase 做 Auth，資料一律經 Worker → Go。
5. **前端不碰任何 service key**；BitGo token 只存在後端、以應用層 AES-256-GCM 加密。

---

## 2. 服務選型（遵守 D2）

| 需求 | 選用 | Always Free? | 被排除的替代與原因 |
|---|---|---|---|
| 運算 | **AWS Lambda**（Go, `provided.al2023`, arm64） | ✅ 1M req + 400k GB-s/月 | EC2（非永久免費） |
| 佇列 | **AWS SQS** + DLQ | ✅ 1M req/月 | — |
| 排程 | **EventBridge Scheduler** | ✅ 14M invocations/月 | — |
| 機密 | **SSM Parameter Store** Standard + SecureString（AWS managed `aws/ssm` key） | ✅（KMS API 20k req/月免費） | Secrets Manager（僅 30 天試用）、自建 KMS CMK（$1/key/月） |
| 日誌／監控 | **CloudWatch** Logs（7 天保留）＋ ≤10 alarms | ✅ 5 GB 日誌、10 alarms | — |
| 同步 API 入口 | **Lambda Function URL**（IAM auth） + Cloudflare Worker | ✅ | **API Gateway**（僅 12 個月免費） |
| 物件儲存 | **Cloudflare R2** | 10 GB-月 | **S3**（僅 12 個月免費） |
| IaC state | **R2（S3-compatible backend）** | — | S3 |
| CI → AWS | GitHub Actions **OIDC** → IAM Role | ✅ | 長期 access key |
| DB + Auth | **Supabase Free**（Tokyo） | 500 MB DB / 5 GB egress | Neon（D3） |
| 前端 | **Cloudflare Pages** | ✅ | — |
| Edge | **Cloudflare Workers Free** | 100k req/日 | — |

> ⚠️ 以上額度為規劃參考，開通前以各官方價格頁為準。AWS 2025 年後的新帳戶 Free Plan 規則需在 Week 0 確認 Always Free 項目是否適用於你的帳戶方案。設定 **AWS Budgets（$1 / $5 警示）**，並記住預算通知 ≠ 硬性停止。
> ⚠️ Cloudflare → AWS 是跨雲呼叫：Worker 需要一組**只允許 `sqs:SendMessage`（單一 queue）與 `lambda:InvokeFunctionUrl` + `lambda:InvokeFunction`（同一個 function；2025 年後 Function URL 需兩者）** 的 IAM user key，存於 Worker secret，每 90 天輪替。這是本架構唯一的長期 AWS 憑證，需列入威脅模型。

---

## 3. Repository 結構（monorepo）

```
VaultSaaS/
├── apps/
│   ├── web/                 # React + Vite + TanStack Query + Supabase Auth JS + wagmi/viem(SIWE)
│   └── edge/                # Cloudflare Worker (TypeScript, Hono)
├── services/core/           # Go module
│   ├── cmd/api/             # Lambda: Function URL handler
│   ├── cmd/worker/          # Lambda: SQS consumer
│   ├── cmd/recon/           # Lambda: scheduled reconciliation
│   ├── internal/domain/     # 狀態機、對帳、審批規則（唯一寫入邏輯）
│   ├── internal/bitgo/      # BitGo REST client（重試、限流、錯誤分類）
│   ├── internal/store/      # pgx（手寫 SQL，未採 sqlc），RLS session 設定
│   ├── internal/crypto/     # token 信封加密
│   └── internal/authz/      # RBAC
├── db/migrations/           # SQL migrations（supabase CLI 或 goose）
├── infra/                   # Terraform：Cloudflare + AWS（見 §3.1）；Supabase 由 supabase CLI 管理
├── docs/                    # ADR、threat model、runbook
└── .github/workflows/       # CI：lint、test、SAST、deps scan、deploy
```

### 3.1 Infrastructure as Code — Terraform（Cloudflare + AWS）

**範圍劃分（誰管什麼）**

| 資源 | 管理工具 | 原因 |
|---|---|---|
| AWS 全部資源 | **Terraform** (`hashicorp/aws ~> 6.0`) | 單一事實來源 |
| Cloudflare：DNS、Worker route／custom domain、WAF／rate limit ruleset、R2 buckets、Pages project、Access | **Terraform** (`cloudflare/cloudflare ~> 5.0`) | 同上 |
| Worker 程式碼與 secrets | **wrangler**（CI） | 避免 secret 進入 Terraform state；程式部署不需 `terraform apply` |
| Lambda 程式碼 | **CI `aws lambda update-function-code`** | Terraform 建立 function（placeholder zip），`lifecycle { ignore_changes = [filename, source_code_hash] }`；infra 與 app 部署解耦 |
| Supabase 專案、schema、Auth 設定 | **supabase CLI**（migrations + `config.toml`） | 依需求不納入 Terraform |

**目錄結構**

```
infra/
├── bootstrap/                 # 一次性，本機以個人憑證執行
│   └── main.tf                # GitHub OIDC provider、CI deploy roles（plan/apply 分權）
├── modules/
│   ├── aws-core/              # Lambda×3 + Function URL、SQS + DLQ、event source mapping、
│   │                          # EventBridge Scheduler、IAM roles、SSM 參數「殼」、log groups、alarms、budgets
│   ├── aws-edge-access/       # 給 Cloudflare Worker 的 IAM user + 最小權限 policy（不建立 access key）
│   └── cloudflare-edge/       # DNS、Worker custom domain、WAF/rate-limit rulesets、
│                              # R2 buckets（reports、audit-anchor + bucket lock、backups + lifecycle）、
│                              # Pages project、Access application（/admin）
├── envs/
│   ├── dev/                   # backend.tf、main.tf（呼叫 modules）、terraform.tfvars
│   └── prod/
└── policy/
    └── free_only.rego         # 原則 0 守門（見下）
```

**State backend：Cloudflare R2（S3-compatible）**

```hcl
terraform {
  required_version = ">= 1.10"
  backend "s3" {
    bucket                      = "vaultops-tfstate"
    key                         = "prod/terraform.tfstate"
    region                      = "auto"
    endpoints                   = { s3 = "https://<ACCOUNT_ID>.r2.cloudflarestorage.com" }
    use_lockfile                = true   # 需驗證 R2 conditional write；不支援則以 CI concurrency group 序列化
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
    use_path_style              = true
  }
}
```
- state bucket 用 `wrangler r2 bucket create` 手動建立（雞生蛋問題），不由 Terraform 管理。
- R2 API token 只授權該 bucket；dev／prod 分不同 `key`。
- State 內不得有 secret：**不使用 `aws_iam_access_key`、不在 Terraform 設 SSM SecureString 的值**（只建參數殼 + `ignore_changes = [value]`，值由 CLI 寫入）。

**原則 0「AWS 不可使用任何付費服務」的強制機制**

1. **資源類型白名單**（CI 對 `terraform show -json tfplan` 跑 conftest/OPA，不在名單即 fail）：
   `aws_iam_*`（`aws_iam_access_key` 除外）、`aws_lambda_function`、`aws_lambda_function_url`、`aws_lambda_permission`、`aws_lambda_event_source_mapping`、`aws_sqs_queue`、`aws_sqs_queue_redrive_policy`、`aws_scheduler_schedule`、`aws_ssm_parameter`、`aws_cloudwatch_log_group`、`aws_cloudwatch_metric_alarm`、`aws_sns_topic`／`aws_sns_topic_subscription`（Always Free：1,000 封 email／月，用於告警通知）、`aws_budgets_budget`。
2. **屬性規則**：
   - `aws_ssm_parameter.tier == "Standard"`（Advanced 收費）；SecureString 不得指定自建 `key_id`（只用 `aws/ssm`）。
   - `aws_cloudwatch_log_group.retention_in_days` 必填且 ≤ 7；alarm 每個環境 ≤ 5（dev + prod 共用免費 10 個）。
   - `aws_lambda_function`：`architectures = ["arm64"]`、`memory_size <= 256`、`timeout <= 30`、不啟用 `tracing_config Active`、不掛 VPC（避免 NAT）、不設 provisioned concurrency。
   - `aws_lambda_event_source_mapping.scaling_config.maximum_concurrency = 2`（限制失控；不用 reserved concurrency，因新帳號總配額可能只有 10）。
   - `aws_budgets_budget` 最多 2 個（$0.01「零花費」警示 + $5 上限警示）。
3. **明確禁止**（即使「看起來免費」）：API Gateway、S3（含 CloudTrail trail 寫 S3——改用免費的 90 天 Event history）、NAT Gateway、EC2、RDS、Secrets Manager、KMS CMK、VPC endpoints、X-Ray active tracing。
4. dev 與 prod **共用同一 AWS 帳號與同一份 Always Free 額度**，以 `name_prefix = "vaultops-${env}"` 區分；不使用 AWS Organizations 多帳號（免費額度不會倍增）。

**CI/CD（GitHub Actions）**

| Workflow | 觸發 | 動作 | 憑證 |
|---|---|---|---|
| `infra-plan` | PR 修改 `infra/**` | `fmt -check`、`validate`、`tflint`、`trivy config`、`plan` → conftest 原則 0 → plan 摘要貼回 PR | AWS：OIDC → `vaultops-tf-plan`（ReadOnly）；CF：read-only API token |
| `infra-apply` | merge 到 `main`（prod 需 environment 人工核准） | `apply` 已審核的 plan artifact | AWS：OIDC → `vaultops-tf-apply`（限 `vaultops-*` 資源）；CF：scoped edit token |
| `deploy-core` | `services/core/**` 變更 | `GOOS=linux GOARCH=arm64` 建 `bootstrap` → zip → `update-function-code` | OIDC → `vaultops-deploy`（僅 `lambda:UpdateFunctionCode`） |
| `deploy-edge` | `apps/edge/**`、`apps/web/**` | `wrangler deploy`、`wrangler pages deploy` | CF scoped token（Cloudflare 不支援 OIDC，token 最小權限 + 90 天輪替） |

**Worker 跨雲 IAM key 的建立流程（不經 Terraform）**
```bash
aws iam create-access-key --user-name vaultops-prod-edge \
  | jq -r '.AccessKey | .AccessKeyId, .SecretAccessKey'   # 分別 pipe 進：
wrangler secret put AWS_ACCESS_KEY_ID      # 與
wrangler secret put AWS_SECRET_ACCESS_KEY
```
輪替：建新 key → 更新 Worker secret → 驗證 → 刪舊 key（runbook 記錄於 `docs/runbooks/rotate-edge-key.md`）。

---

## 4. 資料模型（schema `app`）

```
organizations
├── organization_members (user_id → auth.users, role: owner|admin|approver|viewer)
├── invitations
├── bitgo_connections      (enterprise_id, env, encrypted_token, token_scopes, owner_user_id)
├── wallets                (bitgo_wallet_id, coin, label, last_synced_at)
│   ├── transfers          UNIQUE(provider, wallet_id, transfer_id)
│   └── pending_approvals  (bitgo_approval_id, state, mirrored_at)        -- Phase 2
├── approval_actions       (approval_id, actor, decision, step_up_proof)   -- Phase 2
├── webhook_events         UNIQUE(org_id, provider_event_key), status, attempts
├── expected_payments      (CSV/invoice 匯入：address, coin, amount, reference)
├── reconciliation_runs
│   └── reconciliation_items (status: matched|missing_internal|missing_bitgo|amount_mismatch|status_mismatch)
├── security_alerts
└── audit_logs             (append-only, prev_hash, hash)
user_wallets               (user_id, chain, address, verified_at)  -- SIWE 連結身分
```

完整性規則：
- 所有租戶表含 `organization_id NOT NULL`，RLS policy：`organization_id = current_setting('app.org_id')::uuid`。
- 金額 `NUMERIC(78,0)` 存最小單位（涵蓋 uint256），另存 `coin`、`decimals`；**禁用 float**。
- 帳務異動全在單一 DB transaction；`transfers` 狀態轉移以 `UPDATE … WHERE state = $expected` 做樂觀鎖。
- `audit_logs`：trigger 禁止 UPDATE/DELETE；hash chain；每日將當日 head hash + 匯出檔寫入啟用 **Bucket Lock** 的 R2 bucket（DB 外的防竄改錨點）。
- Supabase Free 無自動備份 → GitHub Actions 每日 `pg_dump` 加密後存 R2，**每月做一次還原演練**。

---

## 5. 關鍵流程

### 5.1 Webhook 入帳（MVP 核心路徑）
1. BitGo → `POST /webhooks/bitgo/{org_webhook_token}`（每租戶隨機不可猜 token）。
2. Worker：Cloudflare WAF 限 BitGo 來源 IP（若官方有公布）、rate limit、body 大小限制；若 BitGo 支援 webhook 簽章則驗章（**Week 0 確認**）。
3. Worker → SQS（message 含 org、raw payload、received_at），立即回 200。
4. `worker` Lambda：`INSERT webhook_events … ON CONFLICT DO NOTHING`（去重）→ 以 BitGo API 重新查詢 transfer → domain 狀態機轉移 → audit log。
5. 失敗 → SQS 重試（visibility timeout + maxReceiveCount=5）→ DLQ → CloudWatch alarm → security_alert。

### 5.2 排程對帳
`recon`（每 15 分鐘，EventBridge Scheduler）：分頁拉取 BitGo transfers（since last cursor）→ 補上 webhook 遺漏 → 與 `expected_payments` 比對 → 產生 `reconciliation_items` → 差異觸發 alert。每日報表 CSV 寫入 R2，以短效 signed URL 下載。

### 5.3 Auth
- Supabase Auth：email magic link / Google OAuth 建帳號。
- 設定頁「連結錢包」：Supabase Web3 (Ethereum/SIWE) identity linking；不允許錢包單獨註冊。
- Worker 以 Supabase JWKS（asymmetric JWT signing keys）驗證 JWT → 將 `sub` 與選定 `org_id` 以 header 傳給 Go；Go 再查 membership 授權（不信任前端給的 role）。

### 5.4 Approve（Phase 2）
- **每位 approver 連結自己的 BitGo access token**（不可共用 service token，否則 BitGo 端看起來全是同一人，破壞多人審批意義）。
- 核准動作需 **step-up**：(a) 用已連結錢包簽 EIP-712 訊息（含 approval id、decision、nonce、expiry）＋ (b) BitGo OTP（若該帳號要求）。
- **範圍限制**：v1 只支援「核准不需 client-side 簽名」的審批（政策變更、custodial／BitGo 簽名的交易審批）；需要 wallet passphrase 簽名的 hot wallet 交易審批**不支援**——保持 D5「不持有私鑰」。
- 所有核准動作寫入 `approval_actions` + `audit_logs`，並以 BitGo API 回讀確認最終狀態。

---

## 6. 時程（10 h/週）

### Week 0（1–2 週）— 帳號與驗證前置
- [ ] 申請 BitGo testnet 帳號、建立 enterprise、取得 access token（最小 scope）。
- [ ] **Email BitGo sales**：第三方 SaaS 使用 API 的授權、正式環境報價、approval API 是否可由第三方 UI 代為操作。
- [ ] 確認 BitGo webhook 簽章機制、來源 IP、rate limits、pending approvals API 行為。
- [ ] 開 AWS 帳號（確認 Free Plan / Always Free 適用性）、Budgets 警示、root MFA、關閉未用 region。
- [ ] 開 Cloudflare、Supabase（Tokyo）、GitHub repo 設定（branch protection、Dependabot）。
- [ ] Terraform 前置：安裝 Terraform ≥ 1.10、`wrangler r2 bucket create vaultops-tfstate`、建立 R2 / Cloudflare scoped API tokens、驗證 R2 對 `use_lockfile` 的支援。
- [ ] 副線：列出 15 個台灣／APAC 潛在客戶名單（Web3 團隊、有穩定幣收款的 SaaS、OTC desk），寄出訪談邀請。

### M1 — Foundation（第 1–2 週，~20h）— ✅ 完成（本機驗收；實際部署待 Week 0 憑證）
- [x] monorepo 骨架 — `30c0228`
- [x] Go `api` Lambda + Function URL adapter、SSM config、`/healthz` — `95d76af`
- [x] `internal/bitgo` 唯讀 client（D5 guard）+ mock BitGo + `GET /v1/wallets` — `15d919b`
- [x] Edge Worker 以 SigV4 呼叫 core — `e593b5d`
- [x] Dashboard 雛形（錢包與餘額）— `eb69472`
- [x] Terraform `bootstrap`（GitHub OIDC + CI roles）— `99f33ab`
- [x] Terraform `aws-core`、`aws-edge-access`、`cloudflare-edge`、`envs/dev|prod`（R2 backend）— `d4b7daa`
- [x] 原則 0 conftest 守門 + `infra-plan` 等 workflows — `92209f1`
- [x] Robot M1 驗收 + compliance 套件（11/11、12/12）— `a546792`
- [ ] `terraform apply` 到真實 AWS / Cloudflare（需 Week 0 帳號與憑證）
- **驗收**：Dashboard 雛形能列出 testnet 錢包與餘額。（以 mock BitGo 通過；真實 testnet 待 Week 0）

### M2 — Ingest Pipeline（第 3–5 週，~30h）— ✅ 完成
- [x] transfers 狀態機與單元測試 — `4cd76a3`
- [x] `app` schema migrations、pgx store、`vaultctl`（含 webhook replay 工具）— `36d0757`
- [x] SQS worker：重新查詢 BitGo、去重、DLQ 前告警 — `2c85da6`
- [x] Edge webhook 入口 → SQS — `90bf454`
- [x] CI：Postgres 測試服務、部署前 migration — `8738c58`
- [x] Robot M2 驗收（10/10）+ compliance（M1+M2 共 36/36）— `f5440d4`
- **驗收**：同一 webhook 重送 10 次只入帳一次；故意讓 BitGo 查詢失敗會進 DLQ 並產生 alert。✅

### M3 — Tenancy, Auth, RBAC, Audit（第 6–7 週，~20h）— ✅ 完成
- [x] 成員、邀請、所有 `app` 表 RLS、audit hash chain + append-only trigger — `4f789ac`
- [x] 每租戶 BitGo 連線（AES-256-GCM 加密，移除共用 token）— `aa515e3`
- [x] Go authz（RBAC）+ org API + SIWE 連結錢包（D4：僅連結身分）— `54ce433`
- [x] Edge 驗證 Supabase JWT（JWKS）並轉送身分；`supabase/config.toml`（Data API 只暴露 `public`、停用 web3 登入）；本機 `mockauth` — `17743db`
- [x] 每日 R2 audit 錨點（`recon` Lambda `audit_anchor` 任務 + 排程；可偵測整條鏈重算的竄改）— `eb48b65`
- [x] Dashboard 登入（Supabase email OTP／本機 mockauth）、組織建立與切換、BitGo 連線、成員邀請、轉帳與告警、連結錢包 — `f04aa8f`
- [x] 修正：本機 anchor 目錄在含 `..` 的路徑下找不到舊錨點 — `0102e11`
- [x] Robot M3 驗收（12/12）+ compliance；M1/M2 套件改為帶身分呼叫（M1–M3 共 51/51）— `1a1093d`
- **驗收**：跨租戶存取整合測試（BOLA/IDOR 案例）全數被拒；修改 audit_logs 被 trigger 拒絕。✅

### M4 — Reconciliation & Reporting（第 8–10 週，~30h）— 🚧 進行中
- [x] Schema：expected_payments、reconciliation_runs/items、transfers.receive_address — `e2c3d12`
- [x] 對帳引擎（BitGo 回補漏掉的 webhook、狀態修正、預期收款比對）— `24dbdcf`
- [x] expected_payments CSV 匯入 API + 對帳 API + `recon` 排程任務 — `4ca487c`
- [ ] 每日報表 CSV → R2（短效 signed URL；防 CSV injection）
- [ ] Dashboard：匯入、執行對帳、差異清單、下載報表
- [ ] Robot M4 驗收 + compliance
- 差異分類：`matched`、`amount_mismatch`、`missing_bitgo`、`missing_internal`（多出入帳）、`status_mismatch`，另加 `missed_webhook`（BitGo 有、帳本沒有 → 回補）。
- **驗收**：在 testnet 製造「漏 webhook」「金額不符」「多出入帳」三種情境，皆被正確分類。

### M5 — Security Alerts & Demo（第 11–12 週，~20h）
- 告警規則：大額、新目的地址、政策變更、DLQ 堆積、token 即將過期。
- SAST（gosec、semgrep）、依賴掃描、threat model 文件、Demo 影片與 README。
- **驗收**：完整展示「BitGo testnet 轉帳 → webhook → 入帳 → 對帳 → 報表 → 告警」。

### Phase 2（MVP 後，約 6–8 週）— Approve + 商業化
- §5.4 審批功能（per-user token、EIP-712 step-up、OTP）。
- 計費：台灣主體無法直接用 Stripe → 評估 **Paddle / Lemon Squeezy（Merchant of Record）** 或 ECPay/TapPay。
- 方案 Starter $19 / Pro $79 / Business $249（**BitGo 報價確認前不上線收費**）。
- ToS、隱私權政策（台灣個資法）、資料處理附錄。

### Phase 3 — 差異化
- MCP Security Assistant（唯讀工具：`list_wallets`、`get_transfer_status`、`find_reconciliation_mismatch`、`summarize_security_alerts`）；BitGo 已有官方 MCP，差異化在跨錢包對帳與安全情境。

---

## 7. 客戶探索副線（D1）

| 時點 | 目標 |
|---|---|
| Week 0–4 | 發出 15 封邀請，完成 ≥3 場需求訪談（現在如何對帳？用什麼工具？誰負責審批？願付多少？） |
| Week 6 | 整理訪談結論，調整 M4 對帳功能優先順序 |
| Week 12 | 對 ≥2 家展示 Demo，取得是否願意試用／付費的明確回覆 |

---

## 8. Go / No-Go 閘門（進入 Phase 2 收費前全部須達成）

1. ✅ Sandbox MVP 完成 M5 驗收，AWS 月費 ≤ US$5。
2. ✅ 取得 BitGo 正式商用條款與報價，且計算後 Pro 方案有正毛利。
3. ✅ ≥3 家目標企業訪談、≥1 家同意試用。
4. ✅ 取得法律意見：Read+Approve 型 SaaS 在台灣是否落入 VASP 登記／洗錢防制義務範圍。

未達成 → 維持作品集定位，不開放收費。

---

## 9. 安全基線（DevSecOps）

- IAM 最小權限：每個 Lambda 獨立 role；Worker 的 IAM user 只有三個 action（`lambda:InvokeFunctionUrl`、`lambda:InvokeFunction`、`sqs:SendMessage`），皆限定單一資源。
- 機密：SSM SecureString（master key）→ 應用層 AES-256-GCM 加密 BitGo tokens，含 `key_version` 以利輪替。
- Edge：Cloudflare WAF managed rules、rate limiting、`/admin` 以 Cloudflare Access 保護。
- 應用：所有輸入 schema 驗證（Worker 用 zod、Go 用強型別 struct + 驗證）；輸出不含 token；CSP、HSTS。
- 供應鏈：Dependabot、`govulncheck`、`npm audit`、gosec、semgrep、鎖定 Actions SHA。
- 日誌：不記錄 token／完整 payload 中的個資；CloudWatch 7 天保留。
- 威脅模型文件（STRIDE），特別處理：webhook 偽造、跨租戶存取、BitGo token 外洩、Worker IAM key 外洩、approval 重放（nonce + expiry）。

---

## 10. 主要風險與對策

| 風險 | 影響 | 對策 |
|---|---|---|
| BitGo 不允許／高價授權第三方 SaaS | 商業模式失效 | Week 0 先問；維持作品集價值 |
| Supabase Free 無備份、無 SLA | 資料遺失 | 每日 pg_dump → R2 + 還原演練；收費前升級 Pro |
| Supabase 5 GB egress 用盡 | 服務受限 | 報表走 R2；Dashboard 分頁與快取；監控用量 |
| 跨雲長期 IAM key | 憑證外洩 | 最小權限、90 天輪替、key 不進 Terraform state、CloudTrail **Event history**（免費 90 天，不建 trail 以免產生 S3 費用）監控 |
| 誤建付費 AWS 資源 | 違反原則 0、意外帳單 | §3.1 conftest 白名單擋在 PR；$0.01 零花費 budget 警示 |
| Terraform state 外洩 | 基礎設施資訊外洩 | state 不含 secret；R2 token 限單一 bucket |
| Approve 功能誤核准 | 資金損失、信任崩潰 | Step-up 簽章 + OTP、per-user token、不支援需私鑰的審批 |
| 10 h/週 進度滑落 | 延期 | 每個 milestone 有可展示的獨立成果；先砍 M5 告警規則數量而非核心路徑 |
| 法規（VASP/AML/個資法） | 無法營運 | Gate 4 法律意見；不託管、不轉帳 |

---

## 11. 尚待決定（不阻擋 M1）

- [ ] 前端是否需要 SSR／SEO 行銷頁（目前：Vite SPA + 另以靜態頁做 landing）。
- [x] Migrations 工具：goose（`vaultctl migrate`，版本表放在非公開 schema `vaultops_meta`）。
- [ ] 計費 MoR 選擇（Paddle vs Lemon Squeezy vs ECPay）— Phase 2 前決定。
- [ ] 第二條鏈的 wallet login（Solana）— 視客戶訪談需求。
