#!/usr/bin/env bash
# Robot Framework acceptance + compliance suites.
#   test/run.sh                       all suites
#   test/run.sh --include m1          one milestone
#   test/run.sh suites/compliance.robot
set -euo pipefail
cd "$(dirname "$0")"
if [[ ! -x .venv/bin/robot ]]; then
  python3 -m venv .venv
  .venv/bin/pip install -q --upgrade pip
fi
.venv/bin/pip install -q -r requirements.txt
export PATH="$(cd .. && pwd)/.tools/bin:$PATH"
export WRANGLER_SEND_METRICS=false
# Default to every suite unless specific .robot files were given.
has_target=false
for a in "$@"; do [[ "$a" == *.robot ]] && has_target=true; done
if ! $has_target; then set -- "$@" suites; fi
exec .venv/bin/robot --outputdir results --loglevel INFO "$@"
