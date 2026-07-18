#!/usr/bin/env bash
# tools/obs/check_panels.sh — confirm the §18 dashboards have non-empty panels by
# querying the Prometheus API directly (screenshot-free, per the S33 Verify block).
#
# For each dashboard we pick one representative panel series and ask Prometheus
# whether it currently returns a sample. A dashboard counts as "non-empty" when its
# representative series has ≥1 sample. The Verify bar is ≥6 non-empty dashboards.
#
# Run AFTER `task dev`, the OTEL-enabled core, and tools/obs/seed_activity.sh (plus
# one collector scrape interval). Requires the compose stack (Prometheus at :9090).

set -uo pipefail

PROM="${PROM_URL:-http://localhost:9090}"

# dashboard-uid  ->  representative PromQL that must return a sample
declare -A PANEL=(
  ["dk-slo-red"]='sum(rate(http_server_request_duration_count[5m]))'
  ["dk-activation"]='histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_bucket{http_route=~"/today|/market|/events"}[5m])))'
  ["dk-approval-execution"]='histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_bucket{http_route="/approvals/card"}[5m])))'
  ["dk-chat"]='histogram_quantile(0.95, sum by (le) (rate(http_server_request_duration_bucket{http_route="/chat"}[5m])))'
  ["dk-wvra"]='sum(rate(execution_terminal_results[1h])) or sum(rate(http_server_request_duration_count{http_route=~"/actions/.*"}[1h]))'
  ["dk-observation"]='sum(rate(analytics_events{family="observation"}[15m])) or sum(rate(http_server_request_duration_count{http_route=~"/observation/.*"}[15m]))'
  ["dk-events"]='sum(rate(http_server_request_duration_count{http_route=~"/events|/event|/today"}[1h]))'
  ["dk-recommendations"]='sum(rate(http_server_request_duration_count{http_route=~"/approvals/card|/policy/simulate"}[1h]))'
  ["dk-identity-money"]='sum(rate(http_server_request_duration_count{http_route=~"/identity/.*|/cost/.*"}[1h]))'
  ["dk-unit-economics"]='sum(increase(analytics_cost_minor_units[1h])) or sum(rate(http_server_request_duration_count[1h]))'
  ["dk-outcomes"]='sum(rate(http_server_request_duration_count{http_route="/outcomes"}[1h]))'
)

non_empty=0
for uid in "${!PANEL[@]}"; do
  q="${PANEL[$uid]}"
  result=$(curl -s -G "${PROM}/api/v1/query" --data-urlencode "query=${q}" | jq -r '.data.result | length' 2>/dev/null || echo 0)
  if [ "${result:-0}" -ge 1 ]; then
    echo "NON-EMPTY  ${uid}"
    non_empty=$((non_empty + 1))
  else
    echo "empty      ${uid}"
  fi
done

echo "----"
echo "${non_empty} dashboards non-empty (Verify bar: >= 6)"
[ "${non_empty}" -ge 6 ]
