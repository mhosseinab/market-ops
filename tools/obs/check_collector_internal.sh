#!/usr/bin/env bash
# tools/obs/check_collector_internal.sh — prove the collector self-telemetry scrape
# seam is LIVE (issue #153, S33). The offline half is
# deploy/otel-collector/collector_internal_telemetry_test.py (config↔target
# consistency, runs in `task obs:validate`); this is the live half that requires
# the compose stack (Prometheus at :9090), same posture as check_panels.sh.
#
# Acceptance (issue #153):
#   * up{job="otel-collector-internal"} == 1  (target reachable + scraped)
#   * a known otelcol_ self-metric is present after startup
#
# Run AFTER `task dev` (collector + Prometheus up, plus one scrape interval).
set -uo pipefail

PROM="${PROM_URL:-http://localhost:9090}"
JOB="otel-collector-internal"

fail() { echo "check_collector_internal: $1" >&2; exit 1; }

# 1. The collector-internal target is UP.
up=$(curl -s -G "${PROM}/api/v1/query" \
  --data-urlencode "query=up{job=\"${JOB}\"}" \
  | jq -r '.data.result[0].value[1] // "absent"' 2>/dev/null || echo "absent")
if [ "$up" != "1" ]; then
  fail "up{job=\"${JOB}\"} is '${up}', expected 1 — collector self-telemetry target is DOWN"
fi
echo "UP         ${JOB} (up == 1)"

# 2. A real otelcol_ self-metric is being scraped through that job.
count=$(curl -s -G "${PROM}/api/v1/query" \
  --data-urlencode "query=count({__name__=~\"otelcol_.+\", job=\"${JOB}\"})" \
  | jq -r '.data.result[0].value[1] // "0"' 2>/dev/null || echo 0)
if [ "${count:-0}" -lt 1 ]; then
  fail "no otelcol_* self-metric present for job '${JOB}' — endpoint bound but empty"
fi
echo "SERIES     ${count} otelcol_* self-metrics present"

echo "----"
echo "collector self-telemetry scrape seam is live (issue #153)"
