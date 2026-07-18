#!/usr/bin/env bash
# tools/obs/seed_activity.sh — generate local gateway traffic so the §18 Grafana
# dashboards render non-empty panels against REAL series (S33 Verify).
#
# This is the "seeded activity script" from the S33 Verify block. It drives the
# running core gateway (task dev + the core binary with OTEL_ENABLED=true) so the
# S33 RED seam (http_server_request_duration_*) and, where the DB is wired, the
# S18 execution counters and S19 analytics/cost series populate. It NEVER performs
# a gated/live/paid operation: it only issues reads and expected-to-fail-closed
# calls against the LOCAL stack, and never approves or executes anything.
#
# Usage:
#   OTEL_ENABLED=true OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 \
#     go run ./services/core/cmd/core   # in one shell (task dev must be up)
#   tools/obs/seed_activity.sh          # in another
#
# Then confirm non-empty panels via the Grafana/Prometheus API (see
# tools/obs/check_panels.sh).

set -uo pipefail

BASE="${CORE_BASE_URL:-http://localhost:8080}"
ITER="${SEED_ITERATIONS:-40}"

echo "seeding activity against ${BASE} (${ITER} iterations)..."

# A spread of routes so the RED histogram gets multiple http_route labels: public,
# auth-gated (expected 401 → 4xx class), and health. The mix populates the rate,
# error, and latency panels on the SLO/RED, activation, approval, and chat boards.
routes=(
  "GET /healthz"
  "GET /today"
  "GET /market"
  "GET /events"
  "GET /approvals/card"
  "GET /briefing"
  "POST /chat"
  "GET /connector/status"
  "GET /outcomes"
  "GET /notifications"
)

for _ in $(seq 1 "${ITER}"); do
  for r in "${routes[@]}"; do
    method="${r%% *}"
    path="${r##* }"
    curl -s -o /dev/null -X "${method}" "${BASE}${path}" \
      -H 'content-type: application/json' \
      --data '{}' >/dev/null 2>&1 || true
  done
done

echo "done. Give the collector one scrape interval (~15s), then run tools/obs/check_panels.sh."
