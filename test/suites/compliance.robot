*** Settings ***
Documentation       Compliance with the plan's Decision Log (§0, D1–D9) and core
...                 principles (§1, 0–5). Static checks over the repository plus the
...                 principle-0 policy run. Re-run after every milestone; checks that
...                 depend on later milestones are tagged with the milestone that
...                 introduces the code they inspect.
Resource            ../resources/common.resource
Test Tags           compliance


*** Variables ***
${FORBIDDEN_AWS}    aws_s3_bucket|aws_api_gateway|aws_apigatewayv2|aws_dynamodb_table|aws_kms_key|aws_secretsmanager|aws_nat_gateway|aws_instance"|aws_db_instance|aws_cloudtrail"|aws_iam_access_key|aws_vpc_endpoint|aws_lambda_provisioned_concurrency


*** Test Cases ***
P0 · D2 AWS Uses Only Always Free Services (policy on offline plans)
    [Tags]    p0    d2    m1
    Run And Expect Success    PATH=${ROOT}/.tools/bin:$PATH infra/scripts/policy-check.sh    timeout=900s

P0 · D2 No Paid AWS Resource Types Appear In Terraform Source
    [Tags]    p0    d2    m1
    No Match In Repo    resource "(${FORBIDDEN_AWS})    infra --include=*.tf

P0 · D2 Lambdas Never Join A VPC Or Enable Paid Features
    [Tags]    p0    d2    m1
    No Match In Repo    vpc_config|provisioned_concurrent|tracing_config    infra/modules --include=*.tf

P0 · D2 Terraform State Uses R2, Not S3
    [Tags]    p0    d2    m1
    ${out}=    Run And Expect Success    grep -rh "skip_s3_checksum\\|use_path_style" infra --include=backend.tf --include=main.tf | wc -l
    Should Be True    int(${out}) >= 6

P1 Ledger State Has A Single Writer (Go domain service only)
    [Tags]    p1    m2
    [Documentation]    Only services/core may write ledger tables; the edge never talks to Postgres.
    No Match In Repo    postgres|supabase-js|@neondatabase|hyperdrive|pg"    apps/edge/src apps/edge/package.json
    No Match In Repo    INSERT INTO app\\.|UPDATE app\\.|DELETE FROM app\\.    apps

P2 Webhooks Are Hints: Worker Re-Fetches Transfers From BitGo
    [Tags]    p2    m2
    Run And Expect Success    grep -rn "GetTransfer(" services/core/internal/ingest
    Run And Expect Success    go test ./internal/ingest/... -run TestWebhookPayloadIsNotTrusted    cwd=${CORE_DIR}

P3 Tenant Tables Carry organization_id And RLS
    [Tags]    p3    m3
    Run And Expect Success    go test ./internal/store/... -run TestEveryTenantTableHasRLS    cwd=${CORE_DIR}

P4 Supabase Data API Does Not Expose The app Schema
    [Tags]    p4    m3
    ${cfg}=    Get File    ${ROOT}/supabase/config.toml
    Should Match Regexp    ${cfg}    (?m)^schemas\\s*=\\s*\\["public"\\]
    Should Not Contain    ${cfg}    "app"

P5 Frontend Holds No Service Keys
    [Tags]    p5    m1
    No Match In Repo    service_role|SERVICE_ROLE|SUPABASE_SERVICE|AWS_SECRET|AKIA[0-9A-Z]{16}|sk_live|BITGO_TOKEN    apps/web/src apps/web/index.html apps/web/.env.example
    Run And Expect Success    cd apps/web && npm run build >/dev/null
    No Match In Repo    service_role|AKIA[0-9A-Z]{16}|BITGO_TOKEN    apps/web/dist

P5 Secrets Never Live In Committed Config
    [Tags]    p5    m1
    No Match In Repo    AWS_SECRET_ACCESS_KEY\\s*=|aws_secret_access_key\\s*=\\s*[A-Za-z0-9/+]{20}    apps/edge/wrangler.toml infra --include=*.tf --include=*.toml
    ${tracked}=    Run And Expect Success    git ls-files | grep -E '(^|/)(\\.env|\\.dev\\.vars|backend\\.r2\\.hcl\\.local|terraform\\.tfvars)$' || true
    Should Be Empty    ${tracked}

D3 Database Is Supabase Postgres (no other datastore)
    [Tags]    d3    m2
    No Match In Repo    dynamodb|mongodb|neon\\.tech|@neondatabase    services apps infra --include=*.go --include=*.ts --include=*.tf
    Run And Expect Success    grep -rq "pgx" services/core/go.mod

D4 Wallet Login Is A Linked Identity Only
    [Tags]    d4    m3
    Run And Expect Success    go test ./internal/identity/... -run TestWalletCannotCreateAccount    cwd=${CORE_DIR}

D5 BitGo Client Can Never Move Funds Or Touch Keys
    [Tags]    d5    m1
    Run And Expect Success    go test ./internal/bitgo/ -run 'TestGuard|TestForbidden'    cwd=${CORE_DIR}
    No Match In Repo    sendcoins|sendmany|tx/build|tx/send|walletPassphrase|prv"    services/core --include=*.go --exclude=guard_test.go --exclude=client.go

D5 Only Pending-Approval Decisions May Write To BitGo
    [Tags]    d5    m1
    ${out}=    Run And Expect Success    grep -rn "http.MethodPost\\|http.MethodPut\\|http.MethodDelete\\|http.MethodPatch" services/core/internal/bitgo/*.go | grep -v _test.go || true
    Should Not Match Regexp    ${out}    MethodPost|MethodDelete|MethodPatch

D6 Infrastructure Lives In Tokyo / APAC
    [Tags]    d6    m1
    ${regions}=    Run And Expect Success    grep -rhoE 'region\\s*=\\s*"[a-z0-9-]+"' infra --include=*.tf | grep -v '"auto"' | sort -u
    Should Match Regexp    ${regions}    ^region\\s*=\\s*"ap-northeast-1"$
    Run And Expect Success    grep -qE 'default\\s*=\\s*"apac"' infra/modules/cloudflare-edge/variables.tf
    Run And Expect Success    grep -q 'AWS_REGION = "ap-northeast-1"' apps/edge/wrangler.toml

D8 Core Is Only Reachable Through The Edge (Function URL uses AWS_IAM)
    [Tags]    d8    m1
    Run And Expect Success    grep -q 'authorization_type = "AWS_IAM"' infra/modules/aws-core/main.tf
    No Match In Repo    authorization_type\\s*=\\s*"NONE"    infra --include=*.tf

D8 Edge IAM User Has Only Invoke And SendMessage
    [Tags]    d8    m1
    ${actions}=    Run And Expect Success    grep -E '^\\s*actions\\s*=' infra/modules/aws-edge-access/main.tf | grep -oE '"[a-z0-9]+:[A-Za-z*]+"' | sort -u | tr '\\n' ' '
    Should Be Equal    ${actions.strip()}    "lambda:InvokeFunction" "lambda:InvokeFunctionUrl" "sqs:SendMessage"

D9 BitGo Defaults To Testnet Everywhere
    [Tags]    d9    m1
    Run And Expect Success    go test ./internal/config/ -run TestProdBitGoURLRequiresProdEnv    cwd=${CORE_DIR}
    No Match In Repo    https://app\\.bitgo\\.com    infra apps --include=*.tf --include=*.toml --include=*.ts
    ${dev}=    Get File    ${ROOT}/infra/envs/dev/main.tf
    Should Contain    ${dev}    https://app.bitgo-test.com


*** Keywords ***
No Match In Repo
    [Documentation]    Fails if the extended regex matches anywhere in the given paths.
    [Arguments]    ${pattern}    ${paths}
    ${result}=    Run Process    bash    -c    grep -rnIE '${pattern}' ${paths} 2>/dev/null | grep -v node_modules || true
    ...    cwd=${ROOT}    stderr=STDOUT
    Should Be Empty    ${result.stdout.strip()}    Forbidden pattern found:\n${result.stdout}
