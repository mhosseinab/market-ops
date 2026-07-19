#!/usr/bin/env bash
# Regression test for issue #10 — dev-stack published ports must bind to loopback.
#
# Always-run, docker-free assertions (parse-based, stdlib Python):
#   1. deploy/compose.dev.yml passes under default env (every port loopback).
#   2. A synthetic all-interfaces config (the pre-fix "5432:5432" form) FAILS —
#      proving the guard catches the vulnerability this issue fixes (TDD RED).
#   3. Setting DK_DEV_BIND_IP=0.0.0.0 flips the real file to a FAILURE — proving
#      remote exposure is a deliberate opt-in, not the default.
#
# Bonus (skipped cleanly if docker is unavailable): render the real file with
# `docker compose config` and assert the postgres/grafana host IP is 127.0.0.1
# by default and 0.0.0.0 only under the opt-in override.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

checker="tools/deploy/check_compose_ports.py"
dev_compose="deploy/compose.dev.yml"

fail() { echo "compose_ports_test: $1" >&2; exit 1; }

# 1. Real file passes under default env.
if ! env -u DK_DEV_BIND_IP python3 "$checker" "$dev_compose" >/dev/null; then
  fail "$dev_compose should bind every published port to loopback under default env"
fi

# 2. RED regression: a pre-fix all-interfaces fixture must be rejected.
fixture="$(mktemp)"
trap 'rm -f "$fixture"' EXIT
cat >"$fixture" <<'YAML'
name: market-ops-dev-regression
services:
  postgres:
    image: postgres:18
    ports:
      - "5432:5432"
  grafana:
    image: grafana/grafana:11.5.0
    ports:
      - "3000:3000"
YAML
if env -u DK_DEV_BIND_IP python3 "$checker" "$fixture" >/dev/null 2>&1; then
  fail "the all-interfaces fixture (5432/3000 on 0.0.0.0) should have been REJECTED"
fi

# 3. Opt-in flip: DK_DEV_BIND_IP=0.0.0.0 must expose (i.e. the guard now fails).
if DK_DEV_BIND_IP=0.0.0.0 python3 "$checker" "$dev_compose" >/dev/null 2>&1; then
  fail "DK_DEV_BIND_IP=0.0.0.0 should flip $dev_compose to non-loopback exposure"
fi

# Bonus: rendered-config assertions when docker is present.
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  default_render="$(env -u DK_DEV_BIND_IP docker compose -f "$dev_compose" config 2>/dev/null || true)"
  if [ -n "$default_render" ]; then
    if printf '%s' "$default_render" | grep -Eq 'host_ip:[[:space:]]*0\.0\.0\.0'; then
      fail "rendered default config exposes a port on 0.0.0.0"
    fi
    if ! printf '%s' "$default_render" | grep -Eq 'host_ip:[[:space:]]*127\.0\.0\.1'; then
      fail "rendered default config does not bind any port to 127.0.0.1"
    fi
    exposed_render="$(DK_DEV_BIND_IP=0.0.0.0 docker compose -f "$dev_compose" config 2>/dev/null || true)"
    if ! printf '%s' "$exposed_render" | grep -Eq 'host_ip:[[:space:]]*0\.0\.0\.0'; then
      fail "DK_DEV_BIND_IP=0.0.0.0 did not flip the rendered config to 0.0.0.0"
    fi
    echo "compose_ports_test: docker compose config assertions passed"
  else
    echo "compose_ports_test: docker present but 'compose config' produced no output; skipped render checks"
  fi
else
  echo "compose_ports_test: docker unavailable; ran parse-based assertions only"
fi

echo "compose_ports_test: OK — dev stack binds published ports to loopback by default"
