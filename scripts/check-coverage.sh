#!/usr/bin/env bash
# This gate operates only on the NSC repository containing this script.
set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

phase="${1:-all}"
if [[ "$#" -gt 1 || ! "$phase" =~ ^(all|prepare|branches|statements|report)$ ]]; then
  echo 'Usage: scripts/check-coverage.sh [prepare|branches|statements|report]' >&2
  exit 2
fi

# Agent validation uses fixed NGINX loopback ports. Run the entire test process
# in a private network namespace so native coverage includes those observations
# and no test can address a live Gateway on the host. Dependencies must already
# be cached (the harness build/vet steps prepare them).
if [[ "${NSC_TEST_ISOLATED_NETWORK:-}" != 1 ]]; then
  for tool in unshare ip; do
    if ! command -v "$tool" >/dev/null; then
      echo "Backend coverage requires $tool for isolated Agent loopback fixtures." >&2
      exit 1
    fi
  done
  exec unshare -Urn -- sh -c 'ip link set lo up && NSC_TEST_ISOLATED_NETWORK=1 exec "$@"' sh bash scripts/check-coverage.sh "$@"
fi

verify_snapshot() {
  "${NODE:-node}" --input-type=module -e '
    import { readFileSync } from "node:fs";
    import { collectChanges, verifySnapshot } from "./scripts/coverage/check-incremental.mjs";
    verifySnapshot(JSON.parse(readFileSync("coverage/source-snapshot.json", "utf8")),
      collectChanges(process.cwd(), process.env.NSC_COVERAGE_BASE || "HEAD"));
  '
}

prepare() {
  mkdir -p coverage || return 1
  rm -f coverage/go-branches.json coverage/go-branches-passed.json coverage/go-statements.out coverage/go-statements-passed.json coverage/incremental.json coverage/source-snapshot.json || return 1
  "${NODE:-node}" scripts/coverage/check-incremental.mjs --thresholds || return 1
  "${NODE:-node}" --test scripts/coverage/check-incremental.test.mjs || return 1
  "${NODE:-node}" scripts/coverage/check-incremental.mjs --snapshot
}

branches() {
  verify_snapshot || return 1
  echo false > coverage/go-branches-passed.json || return 1
  rm -f coverage/go-branches.json || return 1
  local thresholds backend_minimum frontend_minimum result=0
  thresholds="$("${NODE:-node}" scripts/coverage/check-incremental.mjs --thresholds)" || return 1
  read -r backend_minimum frontend_minimum <<< "$thresholds"
  "${GO:-go}" run ./tools/branchcoverage --minimum "$backend_minimum" --output coverage/go-branches.json -- -timeout=15m || result=1
  verify_snapshot || result=1
  if [[ "$result" -eq 0 ]]; then
    echo true > coverage/go-branches-passed.json || return 1
  fi
  return "$result"
}

statements() {
  verify_snapshot || return 1
  echo false > coverage/go-statements-passed.json || return 1
  rm -f coverage/go-statements.out || return 1
  local result=0
  "${GO:-go}" test -count=1 -timeout=15m -coverpkg=./internal/...,./cmd/nsc-controller,./cmd/nsc-agent -coverprofile=coverage/go-statements.out ./internal/... ./cmd/nsc-controller ./cmd/nsc-agent || result=1
  verify_snapshot || result=1
  if [[ "$result" -eq 0 ]]; then
    echo true > coverage/go-statements-passed.json || return 1
  fi
  return "$result"
}

# Current admission is backend business code only, by explicit user scope.
# Frontend remains independently testable through its package scripts.

report() {
  verify_snapshot || return 1
  local result=0
  # A branch threshold failure can leave successful tests and useful partial
  # data. Keep computing incremental evidence without losing that gate failure.
  if ! "${NODE:-node}" --input-type=module -e '
    import { readFileSync } from "node:fs";
    try {
      if (JSON.parse(readFileSync("coverage/go-branches-passed.json", "utf8")) !== true) throw new Error();
    } catch {
      console.error("Missing or unsuccessful Go branch measurement.");
      process.exitCode = 1;
    }
  '; then
    result=1
  fi
  "${NODE:-node}" scripts/coverage/check-incremental.mjs || result=1
  return "$result"
}

status=0
if [[ "$phase" == all ]]; then
  prepare || exit 1
  branches || status=1
  statements || status=1
  report || status=1
else
  "$phase" || status=1
fi
if [[ "$status" -ne 0 ]]; then
  echo 'NSC backend coverage gate failed. Inspect coverage/go-branches.json and coverage/incremental.json.' >&2
fi
exit "$status"
