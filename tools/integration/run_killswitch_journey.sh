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
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

COMPOSE="docker compose -f deploy/compose.test.yml"
export SEEDE2E_PASSWORD="${SEEDE2E_PASSWORD:-s32-integration-owner-password}"

echo "== build the web bundle (default /api base — routes through the Caddy test ingress) =="
(cd apps/web && pnpm run build)

echo "== bring up the integration stack =="
if ! $COMPOSE up -d --wait postgres mockdk llm core web caddy; then
  echo "== compose up --wait failed; dumping llm/core/mockdk logs for diagnosis =="
  $COMPOSE logs llm core mockdk || true
  exit 1
fi

echo "== STOP the LLM plane container (the actual kill-switch condition) =="
$COMPOSE stop llm

echo "== confirm /chat fails closed while screens stay up =="
curl -sf http://localhost:8888/api/healthz >/dev/null

echo "== run the full Playwright journey set against the single Caddy origin =="
(
  cd apps/web
  E2E_WEB_URL="http://localhost:8888" \
  VITE_GATEWAY_BASE_URL="http://localhost:8888/api" \
  E2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}" \
  E2E_PASSWORD="$SEEDE2E_PASSWORD" \
  pnpm exec playwright test
)

echo "== kill-switch journey: ALL Playwright journeys passed with the LLM plane stopped =="

$COMPOSE down -v
