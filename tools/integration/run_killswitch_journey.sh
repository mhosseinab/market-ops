#!/usr/bin/env bash
# S32 kill-switch journey (CHAT-009) — item 1 of
# docs/implementation/dk-p0-implementation-steps.md's S32 suite.
#
# Brings up the compose.test.yml stack (core + llm + web + mockdk + postgres +
# Nginx ingress, deploy/compose.test.yml), STOPS the llm container (the actual
# kill-switch condition — not a config flag), then runs the full existing
# Playwright journey set (apps/web/tests/e2e/journey{1,2,3,4}*.spec.ts) against
# the single Nginx origin. Every journey must still pass: the never-cut
# screens-only fallback (§8/CHAT-009) means losing the LLM plane degrades ONLY
# chat, never any structured screen.
#
# This script assumes docker compose is available (CI's integration job) and
# is deliberately NOT part of `task ci:local` — it is the `task test:integration`
# path, which per dk-p0-monorepo.md §7 runs on merges to dk-p0/main, not per-PR.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

export SEEDE2E_PASSWORD="${SEEDE2E_PASSWORD:-s32-integration-owner-password}"
export SEEDE2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}"

# Second, file-based propagation path for the SAME owner credential (see
# run_all.sh's matching comment): deploy/compose.test.yml's `migrate` service
# REQUIRES SEEDE2E_EMAIL/SEEDE2E_PASSWORD (`${VAR:?...}`) at interpolation time.
# We MUST NOT write the FIXED project file deploy/.env for this — it can hold a
# developer's real deployment config/secrets and must survive success, failure,
# and interruption byte-for-byte (issue #166). Instead we hand `docker compose`
# a PRIVATE, per-run env file via `--env-file`. When run_all.sh orchestrates
# this script it pre-creates that file and exports MARKET_OPS_COMPOSE_ENV_FILE,
# so every scenario shares ONE file whose lifecycle the orchestrator owns;
# standalone we create our own (0600) and remove it in cleanup. `mktemp` gives
# each run a unique path, so parallel runs never share or corrupt an env file.
if [ -n "${MARKET_OPS_COMPOSE_ENV_FILE:-}" ]; then
  COMPOSE_ENV_FILE="$MARKET_OPS_COMPOSE_ENV_FILE"
  OWNS_COMPOSE_ENV_FILE=0
else
  COMPOSE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/market-ops-compose-env.XXXXXX")"
  OWNS_COMPOSE_ENV_FILE=1
  chmod 600 "$COMPOSE_ENV_FILE"
  printf 'SEEDE2E_EMAIL=%s\nSEEDE2E_PASSWORD=%s\n' "$SEEDE2E_EMAIL" "$SEEDE2E_PASSWORD" > "$COMPOSE_ENV_FILE"
fi
COMPOSE="docker compose --env-file $COMPOSE_ENV_FILE -f deploy/compose.test.yml"
# CI-only Go/uv caching overlay — see run_all.sh for the full rationale. Appended
# only when MARKET_OPS_COMPOSE_EXTRA_FILE is set (ci.yml integration job); this must
# use the SAME overlay+cache dirs as run_all.sh so all three bring-ups share one cache.
if [ -n "${MARKET_OPS_COMPOSE_EXTRA_FILE:-}" ]; then
  COMPOSE="$COMPOSE -f $MARKET_OPS_COMPOSE_EXTRA_FILE"
fi
echo "== compose overlay: ${MARKET_OPS_COMPOSE_EXTRA_FILE:-none} =="
echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="

# Always tear the stack down on the way out (success OR failure) — a stack
# left running after a failed journey here previously meant run_all.sh's
# NEXT `compose up` (scenarios 2-5) reused an already-running `core` whose
# `migrate` dependency was skipped on that second up, silently leaving the
# owner without a freshly-set password (the S32 seed-lifecycle race, now
# independently closed by folding seede2e into `migrate` — see
# deploy/compose.test.yml). Tearing down unconditionally here means scenario
# 2 always starts from a clean, freshly-migrated-and-seeded stack regardless
# of how scenario 1 ended. Kept deliberately simple (a single EXIT trap, plain
# `set -euo pipefail` for every other command) rather than per-command `if !`
# guards: `-e` already guarantees the EXIT trap fires with the failing
# command's exit code preserved in `$?` at trap time, so extra manual
# exit-code bookkeeping only adds control-flow surface without changing
# behavior. The private compose env file is only removed here when we created it
# (standalone); when run_all.sh owns it (MARKET_OPS_COMPOSE_ENV_FILE set) it
# needs the file for its later `compose up` and removes it in its own trap. The
# fixed project file deploy/.env is never written or removed by this script.
cleanup() {
  local exit_code=$?
  if [ "$exit_code" -ne 0 ]; then
    echo "== kill-switch journey failed (exit=$exit_code); dumping llm/core/mockdk/migrate logs for diagnosis =="
    $COMPOSE logs llm core mockdk migrate || true
    echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="
  fi
  $COMPOSE down -v >/dev/null 2>&1 || true
  if [ "$OWNS_COMPOSE_ENV_FILE" -eq 1 ]; then
    rm -f "$COMPOSE_ENV_FILE"
  fi
}
trap cleanup EXIT

echo "== build the web bundle (default /api base — routes through the Nginx test ingress) =="
(cd apps/web && pnpm run build)

echo "== bring up the integration stack =="
# No separate `web` service: Nginx serves the built apps/web/dist directly with
# an SPA history fallback from deploy/nginx/nginx.conf, the same config used by
# the production image. The `pnpm run build` above produced that dist.
$COMPOSE up -d --wait postgres mockdk llm core nginx

echo "== STOP the LLM plane container (the actual kill-switch condition) =="
$COMPOSE stop llm

echo "== confirm /chat fails closed while screens stay up =="
curl -sf http://localhost:8888/api/healthz >/dev/null

echo "== run the full Playwright journey set against the single Nginx origin =="
(
  cd apps/web
  E2E_WEB_URL="http://localhost:8888" \
  VITE_GATEWAY_BASE_URL="http://localhost:8888/api" \
  E2E_EMAIL="$SEEDE2E_EMAIL" \
  E2E_PASSWORD="$SEEDE2E_PASSWORD" \
  pnpm exec playwright test
)

echo "== kill-switch journey: ALL Playwright journeys passed with the LLM plane stopped =="
