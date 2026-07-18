#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

output="$({
  env -i \
    PATH="$PATH" \
    HOME="${HOME:-/tmp}" \
    MARKET_OPS_ENV_FILE=/nonexistent/market-ops.env \
    MARKET_OPS_UP_CHECK_ONLY=1 \
    bash "$repo_root/tools/dev/up.sh"
})"

for expected in \
  "APP_ENV=dev" \
  "DATABASE_URL=postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable" \
  "DK_API_BASE_URL=http://localhost:8090" \
  "LLM_PROVIDER_KIND=mock" \
  "VITE_GATEWAY_BASE_URL=/api" \
  "UV_CACHE_DIR=tmp/uv-cache" \
  "CONNECTOR_ENCRYPTION_KEY=generated" \
  "LLM_GATEWAY_TOKEN=generated" \
  "DEV_OWNER_PASSWORD=generated"; do
  if ! grep -Fqx "$expected" <<<"$output"; then
    echo "up_test: missing resolved default: $expected" >&2
    echo "$output" >&2
    exit 1
  fi
done

echo "up_test: clean-environment defaults resolved"
