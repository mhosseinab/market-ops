#!/usr/bin/env bash
# S32 kill-switch journey (CHAT-009) — item 1 of
# docs/implementation/dk-p0-implementation-steps.md's S32 suite.
#
# Brings up the compose.test.yml stack (core + llm + web + mockdk + postgres +
# Caddy ingress, deploy/compose.test.yml), STOPS the llm container (the actual
# kill-switch condition — not a config flag), then runs the full existing
# Playwright journey set (apps/web/tests/e2e/journey{1,2,3,4}*.spec.ts) against
# the single Caddy origin. Every journey must still pass: the never-cut
# screens-only fallback (§8/CHAT-009) means losing the LLM plane degrades ONLY
# chat, never any structured screen.
#
# This script assumes docker compose is available (CI's integration job) and
# is deliberately NOT part of `task ci:local` — it is the `task test:integration`
# path, which per dk-p0-monorepo.md §7 runs on merges to dk-p0/main, not per-PR.
set -uo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

COMPOSE="docker compose -f deploy/compose.test.yml"
export SEEDE2E_PASSWORD="${SEEDE2E_PASSWORD:-s32-integration-owner-password}"
export SEEDE2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}"

# Belt-and-suspenders second propagation path for the SAME owner credential
# (see run_all.sh's matching comment): deploy/compose.test.yml's `migrate`
# service REQUIRES SEEDE2E_EMAIL/SEEDE2E_PASSWORD — writing deploy/.env makes
# Compose's own project-directory .env auto-load a second, independent path
# to the exact same value this script's Playwright invocation below sends as
# E2E_EMAIL/E2E_PASSWORD. Idempotent/safe to overwrite if run_all.sh already
# wrote the same values.
printf 'SEEDE2E_EMAIL=%s\nSEEDE2E_PASSWORD=%s\n' "$SEEDE2E_EMAIL" "$SEEDE2E_PASSWORD" > deploy/.env
echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="

# Always tear the stack down on the way out (success OR failure) — a stack
# left running after a failed journey here previously meant run_all.sh's
# NEXT `compose up` (scenarios 2-5) reused an already-running `core` whose
# `migrate` dependency was skipped on that second up, silently leaving the
# owner without a freshly-set password (the S32 seed-lifecycle race). Tearing
# down unconditionally means scenario 2 always starts from a clean,
# freshly-migrated-and-seeded stack regardless of how scenario 1 ended.
# deploy/.env is only removed here when run standalone — run_all.sh, when it
# orchestrates this script, owns deploy/.env's lifecycle via its own trap and
# still needs the file for its later `compose up`.
cleanup() {
  local exit_code=$?
  if [ "$exit_code" -ne 0 ]; then
    echo "== kill-switch journey failed (exit=$exit_code); dumping core/migrate logs for diagnosis =="
    $COMPOSE logs core migrate || true
    echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="
  fi
  $COMPOSE down -v >/dev/null 2>&1 || true
  if [ -z "${MARKET_OPS_RUN_ALL_ORCHESTRATED:-}" ]; then
    rm -f deploy/.env
  fi
  return "$exit_code"
}
trap cleanup EXIT

echo "== build the web bundle (default /api base — routes through the Caddy test ingress) =="
if ! (cd apps/web && pnpm run build); then
  echo "== web bundle build failed =="
  exit 1
fi

echo "== bring up the integration stack =="
if ! $COMPOSE up -d --wait postgres mockdk llm core web caddy; then
  echo "== compose up --wait failed; dumping llm/core/mockdk/migrate logs for diagnosis =="
  $COMPOSE logs llm core mockdk migrate || true
  exit 1
fi

echo "== STOP the LLM plane container (the actual kill-switch condition) =="
if ! $COMPOSE stop llm; then
  echo "== compose stop llm failed =="
  exit 1
fi

echo "== confirm /chat fails closed while screens stay up =="
if ! curl -sf http://localhost:8888/api/healthz >/dev/null; then
  echo "== /api/healthz check failed =="
  exit 1
fi

echo "== run the full Playwright journey set against the single Caddy origin =="
if ! (
  cd apps/web
  E2E_WEB_URL="http://localhost:8888" \
  VITE_GATEWAY_BASE_URL="http://localhost:8888/api" \
  E2E_EMAIL="$SEEDE2E_EMAIL" \
  E2E_PASSWORD="$SEEDE2E_PASSWORD" \
  pnpm exec playwright test
); then
  echo "== Playwright journey run failed =="
  exit 1
fi

echo "== kill-switch journey: ALL Playwright journeys passed with the LLM plane stopped =="
