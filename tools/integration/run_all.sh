#!/usr/bin/env bash
# task test:integration — the S32 cross-plane integration suite
# (dk-p0-implementation-steps.md §"S32 — Cross-plane integration + adversarial +
# kill-switch suites"). Compose-based (deploy/compose.test.yml): core + llm +
# web + mockdk + postgres, fronted by a Caddy ingress unifying web+gateway
# under one origin (§19.3 topology).
#
# Runs the five S32 suites and prints one PASS/FAIL line per scenario:
#   1. kill-switch journey (CHAT-009)               — tools/integration/run_killswitch_journey.sh
#   2. adversarial containment replay (CHAT-041/045) — tools/integration/replay_adversarial.py
#   3. §16 edge-case fixtures                        — go test (internal/httpapi/system_edge_cases_test.go)
#   4. permission parity (CHAT-064)                  — go test (internal/httpapi/system_permission_parity_test.go)
#   5. system duplicate-write (EXE-002)               — go test (internal/httpapi/system_duplicate_write_test.go)
#
# Wired as a CI job on MERGES to dk-p0/main (not per-PR) — see .github/workflows/ci.yml.
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

COMPOSE="docker compose -f deploy/compose.test.yml"
export SEEDE2E_PASSWORD="${SEEDE2E_PASSWORD:-s32-integration-owner-password}"
export SEEDE2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}"

declare -A RESULT
FAILED=0

report() {
  local name="$1" status="$2"
  RESULT["$name"]="$status"
  if [ "$status" != "PASS" ]; then FAILED=1; fi
}

cleanup() {
  $COMPOSE down -v >/dev/null 2>&1 || true
}
trap cleanup EXIT

# --- scenario 1: kill-switch journey (stops+restarts its own stack) --------
echo "### 1/5 kill-switch journey (CHAT-009) ###"
if bash tools/integration/run_killswitch_journey.sh; then
  report "1. kill-switch journey (CHAT-009)" "PASS"
else
  report "1. kill-switch journey (CHAT-009)" "FAIL"
fi

# --- bring the stack back up (with the LLM plane LIVE) for scenarios 2-5 ---
echo "### bringing the stack up (LLM plane live) for scenarios 2-5 ###"
$COMPOSE up -d --wait postgres mockdk llm core web caddy
DATABASE_URL="postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable"
export DATABASE_URL

# --- scenario 2: adversarial containment replay (CHAT-041/045) ------------
echo "### 2/5 adversarial containment replay (CHAT-041/045) ###"
if uv run --group dev python3 tools/integration/replay_adversarial.py \
    --core-url http://localhost:8888/api \
    --email "$SEEDE2E_EMAIL" --password "$SEEDE2E_PASSWORD" \
    --account-id 00000000-0000-0000-0000-000000000003 \
    --fuzz 3 \
    --report /tmp/s32_adversarial_report.json; then
  report "2. adversarial containment replay (CHAT-041/045)" "PASS"
else
  report "2. adversarial containment replay (CHAT-041/045)" "FAIL"
fi

# --- scenarios 3-5: Go system tests against the compose Postgres ----------
echo "### 3/5 §16 edge-case fixtures ###"
if (cd services/core && GOWORK=off go test ./internal/httpapi/... -run 'TestEdgeCase' -v); then
  report "3. §16 edge-case fixtures" "PASS"
else
  report "3. §16 edge-case fixtures" "FAIL"
fi

echo "### 4/5 permission parity (CHAT-064) ###"
if (cd services/core && GOWORK=off go test ./internal/httpapi/... -run 'TestPermissionParity' -v); then
  report "4. permission parity (CHAT-064)" "PASS"
else
  report "4. permission parity (CHAT-064)" "FAIL"
fi

echo "### 5/5 system duplicate-write (EXE-002) ###"
if (cd services/core && GOWORK=off go test ./internal/httpapi/... -run 'TestSystemDuplicateWrite' -race -v); then
  report "5. system duplicate-write (EXE-002)" "PASS"
else
  report "5. system duplicate-write (EXE-002)" "FAIL"
fi

echo
echo "=== S32 test:integration — per-scenario report ==="
for name in "1. kill-switch journey (CHAT-009)" \
            "2. adversarial containment replay (CHAT-041/045)" \
            "3. §16 edge-case fixtures" \
            "4. permission parity (CHAT-064)" \
            "5. system duplicate-write (EXE-002)"; do
  printf '%-55s %s\n' "$name" "${RESULT[$name]:-MISSING}"
done

exit $FAILED
