#!/usr/bin/env bash
# task test:integration — the S32 cross-plane integration suite
# (dk-p0-implementation-steps.md §"S32 — Cross-plane integration + adversarial +
# kill-switch suites"). Compose-based (deploy/compose.test.yml): core + llm +
# web + mockdk + postgres, fronted by an Nginx ingress unifying web+gateway
# under one origin (§19.3 topology).
#
# Runs the S32 suites and prints one PASS/FAIL line per scenario:
#   1. kill-switch journey (CHAT-009)               — tools/integration/run_killswitch_journey.sh
#   2. adversarial containment replay (CHAT-041/045) — tools/integration/replay_adversarial.py
#   3. §16 edge-case gate (manifest-driven)          — go run ./cmd/section16gate (tools/integration/section16_manifest.json)
#   4. permission parity (CHAT-064)                  — go test (internal/httpapi/system_permission_parity_test.go)
#   5. system duplicate-write (EXE-002)               — go test (internal/httpapi/system_duplicate_write_test.go)
#   6. cold-start LLM-unhealthy isolation (CHAT-009) — tools/integration/run_coldstart_llm_unhealthy_journey.sh
#
# Wired as a CI job on MERGES to dk-p0/main (not per-PR) — see .github/workflows/ci.yml.
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

export SEEDE2E_PASSWORD="${SEEDE2E_PASSWORD:-s32-integration-owner-password}"
export SEEDE2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}"

# Belt-and-suspenders second propagation path for the SAME owner credential:
# deploy/compose.test.yml's `core`/`migrate` services REQUIRE SEEDE2E_EMAIL/
# SEEDE2E_PASSWORD (`${VAR:?...}`, no in-YAML default — a diverging default
# there previously let the container-seeded password silently differ from
# what the host-side replay/Playwright send, producing a confusing 401 with
# an otherwise-healthy stack). We MUST NOT write the FIXED project file
# deploy/.env for this — it can hold a developer's real deployment config and
# secrets, and must survive success, failure, and interruption byte-for-byte
# (issue #166). Instead we hand `docker compose` a PRIVATE, per-run env file via
# `--env-file` (0600), giving Compose a second, independent path to the exact
# same value as this shell's export so `docker compose up` cannot resolve a
# different credential than $SEEDE2E_EMAIL/$SEEDE2E_PASSWORD below use. The path
# is exported as MARKET_OPS_COMPOSE_ENV_FILE so the child journey scripts reuse
# this one orchestrator-owned file; this trap removes it at the very end. mktemp
# gives a unique path, so parallel `run_all.sh` invocations never collide.
COMPOSE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/market-ops-compose-env.XXXXXX")"
chmod 600 "$COMPOSE_ENV_FILE"
printf 'SEEDE2E_EMAIL=%s\nSEEDE2E_PASSWORD=%s\n' "$SEEDE2E_EMAIL" "$SEEDE2E_PASSWORD" > "$COMPOSE_ENV_FILE"
export MARKET_OPS_COMPOSE_ENV_FILE="$COMPOSE_ENV_FILE"
COMPOSE="docker compose --env-file $COMPOSE_ENV_FILE -f deploy/compose.test.yml"
echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="

declare -A RESULT
FAILED=0

report() {
  local name="$1" status="$2"
  RESULT["$name"]="$status"
  if [ "$status" != "PASS" ]; then FAILED=1; fi
}

cleanup() {
  $COMPOSE down -v >/dev/null 2>&1 || true
  # Remove ONLY the private, per-run env file this orchestrator created — never
  # the fixed project file deploy/.env (issue #166).
  rm -f "$COMPOSE_ENV_FILE"
}
trap cleanup EXIT

# --- scenario 1: kill-switch journey (stops+restarts its own stack) --------
echo "### 1/6 kill-switch journey (CHAT-009) ###"
# The exported MARKET_OPS_COMPOSE_ENV_FILE tells the child to reuse this
# orchestrator's private env file (and not remove it), so scenarios 2-5's later
# `compose up` still resolve the same credential; this orchestrator's own trap
# removes it at the end.
if bash tools/integration/run_killswitch_journey.sh; then
  report "1. kill-switch journey (CHAT-009)" "PASS"
else
  report "1. kill-switch journey (CHAT-009)" "FAIL"
fi

# --- bring the stack back up (with the LLM plane LIVE) for scenarios 2-5 ---
echo "### bringing the stack up (LLM plane live) for scenarios 2-5 ###"
# No separate `web` service — Nginx serves apps/web/dist directly (see
# deploy/compose.test.yml). Scenarios 2-5 drive only /api, so the web bundle
# is irrelevant here; scenario 1 (run_killswitch_journey.sh) is what builds it.
if ! $COMPOSE up -d --wait postgres mockdk llm core nginx; then
  echo "== compose up --wait failed; dumping llm/core/mockdk/migrate logs for diagnosis =="
  $COMPOSE logs llm core mockdk migrate || true
  report "2. adversarial containment replay (CHAT-041/045)" "FAIL"
  report "3. §16 edge-case fixtures" "FAIL"
  report "4. permission parity (CHAT-064)" "FAIL"
  report "5. system duplicate-write (EXE-002)" "FAIL"
  echo
  echo "=== S32 test:integration — per-scenario report ==="
  for name in "1. kill-switch journey (CHAT-009)" \
              "2. adversarial containment replay (CHAT-041/045)" \
              "3. §16 edge-case fixtures" \
              "4. permission parity (CHAT-064)" \
              "5. system duplicate-write (EXE-002)"; do
    printf '%-55s %s\n' "$name" "${RESULT[$name]:-MISSING}"
  done
  exit 1
fi
DATABASE_URL="postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable"
export DATABASE_URL

# --- scenario 2: adversarial containment replay (CHAT-041/045) ------------
echo "### 2/6 adversarial containment replay (CHAT-041/045) ###"
if uv run --group dev python3 tools/integration/replay_adversarial.py \
    --core-url http://localhost:8888/api \
    --email "$SEEDE2E_EMAIL" --password "$SEEDE2E_PASSWORD" \
    --account-id 00000000-0000-0000-0000-000000000003 \
    --fuzz 3 \
    --report /tmp/s32_adversarial_report.json; then
  report "2. adversarial containment replay (CHAT-041/045)" "PASS"
else
  echo "== adversarial replay failed; dumping core/migrate logs for diagnosis =="
  $COMPOSE logs core migrate || true
  echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="
  report "2. adversarial containment replay (CHAT-041/045)" "FAIL"
fi

# --- scenarios 3-5: Go system tests against the compose Postgres ----------
# Scenario 3 is now the manifest-driven §16 edge-case GATE (issue #164): instead
# of `go test -run 'TestEdgeCase'` (which selected only the three TestEdgeCase*
# functions and silently ignored every other required §16 row), the gate reads
# tools/integration/section16_manifest.json, cross-checks it against the PRD §16
# table, and executes every mapped test explicitly — failing if any canonical row
# is unclassified, any mapped test is renamed/removed (zero -list matches), or any
# mapped test is skipped rather than run. It reports one line per §16 row.
echo "### 3/6 §16 edge-case gate (manifest-driven, issue #164) ###"
if (cd services/core && GOWORK=off go run ./cmd/section16gate); then
  report "3. §16 edge-case fixtures" "PASS"
else
  report "3. §16 edge-case fixtures" "FAIL"
fi

echo "### 4/6 permission parity (CHAT-064) ###"
if (cd services/core && GOWORK=off go test ./internal/httpapi/... -run 'TestPermissionParity' -v); then
  report "4. permission parity (CHAT-064)" "PASS"
else
  report "4. permission parity (CHAT-064)" "FAIL"
fi

echo "### 5/6 system duplicate-write (EXE-002) ###"
if (cd services/core && GOWORK=off go test ./internal/httpapi/... -run 'TestSystemDuplicateWrite' -race -v); then
  report "5. system duplicate-write (EXE-002)" "PASS"
else
  report "5. system duplicate-write (EXE-002)" "FAIL"
fi

# --- scenario 6: cold-start LLM-unhealthy isolation (brings up its OWN stack) ---
# Scenarios 2-5 left a stack up WITH the LLM plane live; the cold-start journey
# must boot with the LLM plane ABSENT from the first boot, so tear that stack
# down first. Like scenario 1 this child owns its own up/down lifecycle (its EXIT
# trap `compose down -v`) and, via the exported MARKET_OPS_COMPOSE_ENV_FILE,
# reuses this orchestrator's private env file without removing it.
echo "### tearing the (llm-live) stack down before the cold-start scenario ###"
$COMPOSE down -v >/dev/null 2>&1 || true
echo "### 6/6 cold-start LLM-unhealthy isolation (CHAT-009) ###"
if bash tools/integration/run_coldstart_llm_unhealthy_journey.sh; then
  report "6. cold-start LLM-unhealthy isolation (CHAT-009)" "PASS"
else
  report "6. cold-start LLM-unhealthy isolation (CHAT-009)" "FAIL"
fi

echo
echo "=== S32 test:integration — per-scenario report ==="
for name in "1. kill-switch journey (CHAT-009)" \
            "2. adversarial containment replay (CHAT-041/045)" \
            "3. §16 edge-case fixtures" \
            "4. permission parity (CHAT-064)" \
            "5. system duplicate-write (EXE-002)" \
            "6. cold-start LLM-unhealthy isolation (CHAT-009)"; do
  printf '%-55s %s\n' "$name" "${RESULT[$name]:-MISSING}"
done

exit $FAILED
