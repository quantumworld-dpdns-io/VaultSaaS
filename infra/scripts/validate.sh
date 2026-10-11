#!/usr/bin/env bash
# terraform fmt + validate for every root module, without a backend or credentials.
set -euo pipefail
cd "$(dirname "$0")/.."
terraform fmt -check -recursive .
for dir in bootstrap envs/dev envs/prod; do
  echo "== validate $dir"
  terraform -chdir="$dir" init -backend=false -input=false -no-color >/dev/null
  terraform -chdir="$dir" validate -no-color
done
