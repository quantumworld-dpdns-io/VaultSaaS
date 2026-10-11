#!/usr/bin/env bash
# Principle-0 guard. Produces a plan for each environment and evaluates it with
# conftest against infra/policy.
#
#   policy-check.sh            offline: plan with mock credentials and fixture
#                              variables (no cloud access needed; nothing applied)
#   policy-check.sh <plan.json>  evaluate an existing `terraform show -json` file
#                              (used by the infra-plan workflow with real plans)
set -euo pipefail
cd "$(dirname "$0")/.."
INFRA=$(pwd)
CONFTEST=${CONFTEST:-conftest}

echo "== policy unit tests"
"$CONFTEST" verify --policy policy

if [[ $# -gt 0 ]]; then
  for f in "$@"; do
    echo "== conftest $f"
    "$CONFTEST" test --policy policy --all-namespaces "$f"
  done
  exit 0
fi

for env in dev prod; do
  work="envs/_policy_${env}"
  rm -rf "$work"
  cp -R "envs/$env" "$work"
  rm -rf "$work/.terraform" "$work/terraform.tfstate"
  # allowed_account_ids needs a live STS call; drop it from this throwaway copy only.
  sed -i.bak '/allowed_account_ids/d' "$work/main.tf" && rm -f "$work/main.tf.bak"
  # Override the backend and providers so the plan needs no credentials and
  # makes no API calls for resources being created.
  cat > "$work/zz_policy_override.tf" <<'EOF'
terraform {
  backend "local" {}
}
provider "aws" {
  region                      = "ap-northeast-1"
  access_key                  = "mock"
  secret_key                  = "mock"
  skip_credentials_validation = true
  skip_requesting_account_id  = true
  skip_metadata_api_check     = true
  skip_region_validation      = true
}
provider "cloudflare" {
  api_token = "mockmockmockmockmockmockmockmockmockmock"
}
EOF
  cat > "$work/policy.auto.tfvars" <<'EOF'
aws_account_id        = "123456789012"
cloudflare_account_id = "0123456789abcdef0123456789abcdef"
cloudflare_zone_id    = "fedcba9876543210fedcba9876543210"
domain                = "vaultops.example"
alert_email           = "ops@vaultops.example"
admin_emails          = ["admin@vaultops.example"]
EOF
  echo "== plan $env (offline)"
  terraform -chdir="$work" init -input=false -no-color >/dev/null
  terraform -chdir="$work" plan -input=false -refresh=false -lock=false -no-color -out=tfplan >/dev/null
  terraform -chdir="$work" show -json tfplan > "$work/tfplan.json"
  echo "== conftest $env"
  "$CONFTEST" test --policy policy --all-namespaces "$work/tfplan.json"
  rm -rf "$INFRA/$work"
done
