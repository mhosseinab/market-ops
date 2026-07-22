#!/usr/bin/env bash
# S32 cold-start LLM-unhealthy isolation journey (CHAT-009) — the cold-start
# complement to tools/integration/run_killswitch_journey.sh (which proves the
# WARM case: a fully-healthy stack whose LLM is then STOPPED).
#
# This script proves the COLD case the warm-stop journey cannot: the LLM plane
# is ABSENT from the very first boot. It brings up ONLY the structured plane
# (postgres + migrate + mockdk + core + Nginx) and deliberately never starts
# `llm`. Because deploy/compose.test.yml's `core` service no longer hard-depends
# on `llm: condition: service_healthy` (removing that edge is the S32 fix), core
# and Nginx reach healthy WITHOUT any healthy LLM, and every structured route is
# served normally. Only /chat fails closed — the never-cut screens-only fallback
# (§8/CHAT-009): a chat-plane failure must never take down structured screens.
#
# LLM_SERVICE_URL still points at the (now unreachable) http://llm:8100, so the
# gateway wires the LLM seam but every StartTurn fails, and /chat returns the
# documented bounded structured-unavailable state (HTTP 503, reason
# provider_unavailable — services/core/internal/httpapi/chat.go).
#
# Finally it proves RECOVERY: bringing `llm` up healthy LATER (without recreating
# core/nginx) makes /chat succeed again — the structured plane is never restarted.
#
# Like run_killswitch_journey.sh this assumes docker compose is available (CI's
# integration job) and is NOT part of `task ci:local`; it runs under
# `task test:integration` (dk-p0-monorepo.md §7) on merges to dk-p0/main.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT_DIR"

export SEEDE2E_PASSWORD="${SEEDE2E_PASSWORD:-s32-integration-owner-password}"
export SEEDE2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}"
# shellcheck source=tools/integration/configure_cache.sh
source "$ROOT_DIR/tools/integration/configure_cache.sh"

BASE="http://localhost:8888"
COOKIE_JAR="$(mktemp)"

# Same second credential-propagation path as run_killswitch_journey.sh /
# run_all.sh: deploy/compose.test.yml's `migrate` one-shot REQUIRES
# SEEDE2E_EMAIL/SEEDE2E_PASSWORD (`${VAR:?...}`) at interpolation time. We MUST
# NOT write the FIXED project file deploy/.env for this — it can hold a
# developer's real config/secrets and must survive byte-for-byte (issue #166).
# Instead we hand `docker compose` a PRIVATE, per-run env file via `--env-file`;
# when run_all.sh orchestrates this script it pre-creates that file and exports
# MARKET_OPS_COMPOSE_ENV_FILE (shared, orchestrator-owned lifecycle), otherwise
# we create our own (0600) and remove it in cleanup. `mktemp` keeps parallel
# runs from sharing or corrupting an env file.
if [ -n "${MARKET_OPS_COMPOSE_ENV_FILE:-}" ]; then
  COMPOSE_ENV_FILE="$MARKET_OPS_COMPOSE_ENV_FILE"
  OWNS_COMPOSE_ENV_FILE=0
else
  COMPOSE_ENV_FILE="$(mktemp "${TMPDIR:-/tmp}/market-ops-compose-env.XXXXXX")"
  OWNS_COMPOSE_ENV_FILE=1
  chmod 600 "$COMPOSE_ENV_FILE"
  printf 'SEEDE2E_EMAIL=%s\nSEEDE2E_PASSWORD=%s\n' "$SEEDE2E_EMAIL" "$SEEDE2E_PASSWORD" > "$COMPOSE_ENV_FILE"
fi
COMPOSE="docker compose --env-file $COMPOSE_ENV_FILE -f deploy/compose.test.yml -f $MARKET_OPS_COMPOSE_EXTRA_FILE"
echo "== compose overlay: $MARKET_OPS_COMPOSE_EXTRA_FILE =="
echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="

# Unconditional teardown on the way out (success OR failure); see
# run_killswitch_journey.sh for the full lifecycle rationale. The private compose
# env file is only removed when we created it (standalone) — under run_all.sh
# orchestration (MARKET_OPS_COMPOSE_ENV_FILE set) the orchestrator owns its
# lifecycle. The fixed project file deploy/.env is never written or removed here.
cleanup() {
  local exit_code=$?
  if [ "$exit_code" -ne 0 ]; then
    echo "== cold-start journey failed (exit=$exit_code); dumping core/nginx/mockdk/migrate logs for diagnosis =="
    $COMPOSE logs core nginx mockdk migrate || true
    echo "== seeded owner: email=${SEEDE2E_EMAIL} password_len=${#SEEDE2E_PASSWORD} (value never logged) =="
  fi
  rm -f "$COOKIE_JAR" || true
  $COMPOSE down -v >/dev/null 2>&1 || true
  if [ "$OWNS_COMPOSE_ENV_FILE" -eq 1 ]; then
    rm -f "$COMPOSE_ENV_FILE"
  fi
}
trap cleanup EXIT

echo "== build the web bundle (default /api base — routes through the Nginx test ingress) =="
(cd apps/web && pnpm run build)

echo "== bring up the STRUCTURED plane ONLY — llm is deliberately NEVER started =="
# Proven killswitch bring-up line MINUS `llm`: the LLM plane is absent from the
# first boot. migrate + core + Nginx still come up (migrate is a dependency of
# mockdk/core; it is a one-shot that completes). If core still hard-depended on
# `llm: service_healthy` this `up --wait` would either pull llm in and block on
# it or never complete — the exact defect this journey guards against.
$COMPOSE up -d --wait postgres mockdk core nginx

echo "== ASSERT core+Nginx are healthy through the ingress with NO llm (structured plane serving) =="
curl -sf "${BASE}/api/healthz" >/dev/null
echo "PASS: /api/healthz 200 — core reachable through Nginx without a healthy LLM"

echo "== ASSERT a structured screen is served (SPA history fallback) with NO llm =="
curl -sf -o /dev/null "${BASE}/"
curl -sf -o /dev/null "${BASE}/onboarding"
echo "PASS: SPA index served (/, /onboarding) — screens usable without the LLM plane"

echo "== log in (session cookie) so /chat reaches the gateway handler, not the 401 gate =="
login_code="$(curl -s -o /dev/null -w '%{http_code}' -c "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  --data "{\"email\":\"${SEEDE2E_EMAIL}\",\"password\":\"${SEEDE2E_PASSWORD}\"}" \
  "${BASE}/api/auth/login")"
if [ "$login_code" != "200" ]; then
  echo "FAIL: login expected 200, got ${login_code}"
  exit 1
fi
echo "PASS: authenticated owner session established"

echo "== ASSERT /chat fails closed: HTTP 503 + reason provider_unavailable (bounded structured unavailable state) =="
chat_body="$(mktemp)"
chat_code="$(curl -s -o "$chat_body" -w '%{http_code}' -b "$COOKIE_JAR" \
  -H 'Content-Type: application/json' \
  --data '{"message":"is this screen still up?","locale":"fa-IR"}' \
  "${BASE}/api/chat")"
if [ "$chat_code" != "503" ]; then
  echo "FAIL: /chat expected 503 (provider_unavailable), got ${chat_code}; body:"
  cat "$chat_body" || true
  rm -f "$chat_body"
  exit 1
fi
if ! grep -q 'provider_unavailable' "$chat_body"; then
  echo "FAIL: /chat 503 body missing reason provider_unavailable; body:"
  cat "$chat_body" || true
  rm -f "$chat_body"
  exit 1
fi
rm -f "$chat_body"
echo "PASS: /chat -> 503 provider_unavailable while every structured route stayed up"

echo "== RECOVERY: bring the LLM plane up healthy WITHOUT recreating core/nginx =="
core_before="$($COMPOSE ps -q core)"
nginx_before="$($COMPOSE ps -q nginx)"
$COMPOSE up -d --wait llm
core_after="$($COMPOSE ps -q core)"
nginx_after="$($COMPOSE ps -q nginx)"
if [ "$core_before" != "$core_after" ] || [ "$nginx_before" != "$nginx_after" ]; then
  echo "FAIL: structured plane was recreated during recovery (core ${core_before}->${core_after}, nginx ${nginx_before}->${nginx_after})"
  exit 1
fi
echo "PASS: llm now healthy; core/nginx container ids unchanged (structured plane never restarted)"

echo "== ASSERT /chat now succeeds (2xx SSE) — recovery without touching the structured plane =="
recovered_code="$(curl -s -o /dev/null -w '%{http_code}' -b "$COOKIE_JAR" --max-time 30 \
  -H 'Content-Type: application/json' \
  --data '{"message":"and now that chat is back?","locale":"fa-IR"}' \
  "${BASE}/api/chat")"
case "$recovered_code" in
  2*)
    echo "PASS: /chat -> ${recovered_code} after LLM recovery (mock provider), structured plane untouched" ;;
  *)
    echo "FAIL: /chat after recovery expected 2xx, got ${recovered_code}"
    exit 1 ;;
esac

echo "== cold-start LLM-unhealthy journey: structured plane stayed up with NO llm, /chat failed closed, and chat recovered without a restart =="
