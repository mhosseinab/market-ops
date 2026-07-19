#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

env_file="${MARKET_OPS_ENV_FILE:-$repo_root/.env}"
if [[ -f "$env_file" ]]; then
  set -a
  # shellcheck disable=SC1090
  . "$env_file"
  set +a
fi

# `task up` is always a local-development command. Optional .env values may
# customize non-sensitive settings, while these defaults make a clean checkout
# runnable without copying or sourcing any environment file.
export APP_ENV=dev
export HTTP_ADDR=:8080
export DATABASE_URL="postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable"
export DK_API_BASE_URL="${DK_API_BASE_URL:-http://localhost:8090}"
export LLM_SERVICE_URL=http://127.0.0.1:8100
export LLM_PROVIDER_KIND="${LLM_PROVIDER_KIND:-mock}"
export APP_BASE_URL="${APP_BASE_URL:-http://localhost:5173}"
export VITE_GATEWAY_BASE_URL=/api
export UV_CACHE_DIR="${UV_CACHE_DIR:-$repo_root/tmp/uv-cache}"

is_placeholder() {
  [[ -z "$1" || "$1" == CHANGE_ME_* ]]
}

if [[ "${MARKET_OPS_UP_CHECK_ONLY:-}" == "1" ]]; then
  printf '%s\n' \
    "APP_ENV=$APP_ENV" \
    "DATABASE_URL=$DATABASE_URL" \
    "DK_API_BASE_URL=$DK_API_BASE_URL" \
    "LLM_PROVIDER_KIND=$LLM_PROVIDER_KIND" \
    "VITE_GATEWAY_BASE_URL=$VITE_GATEWAY_BASE_URL" \
    "UV_CACHE_DIR=tmp/uv-cache"
  if is_placeholder "${CONNECTOR_ENCRYPTION_KEY:-}"; then
    echo "CONNECTOR_ENCRYPTION_KEY=generated"
  else
    echo "CONNECTOR_ENCRYPTION_KEY=provided"
  fi
  if is_placeholder "${LLM_GATEWAY_TOKEN:-}"; then
    echo "LLM_GATEWAY_TOKEN=generated"
  else
    echo "LLM_GATEWAY_TOKEN=provided"
  fi
  if is_placeholder "${SEEDE2E_PASSWORD:-}"; then
    echo "DEV_OWNER_PASSWORD=generated"
  else
    echo "DEV_OWNER_PASSWORD=provided"
  fi
  if [[ -z "${GF_SECURITY_ADMIN_PASSWORD:-}" ]]; then
    echo "GRAFANA_ADMIN_PASSWORD=generated"
  else
    echo "GRAFANA_ADMIN_PASSWORD=provided"
  fi
  exit 0
fi

for required_tool in curl openssl goose river task pnpm uv go docker; do
  if ! command -v "$required_tool" >/dev/null 2>&1; then
    echo "task up: missing required tool: $required_tool" >&2
    exit 1
  fi
done

mkdir -p tmp
chmod 700 tmp
mkdir -p "$UV_CACHE_DIR"

read_or_create_secret() {
  local secret_path="$1"
  local secret_kind="$2"
  local value

  if [[ -s "$secret_path" ]]; then
    IFS= read -r value <"$secret_path"
  else
    case "$secret_kind" in
      connector) value="$(openssl rand -base64 32 | tr -d '\n')" ;;
      token) value="$(openssl rand -hex 32)" ;;
      password) value="$(openssl rand -base64 24 | tr -d '\n')" ;;
      *) echo "task up: unknown secret kind: $secret_kind" >&2; return 1 ;;
    esac
    umask 077
    printf '%s\n' "$value" >"$secret_path"
    chmod 600 "$secret_path"
  fi

  printf '%s' "$value"
}

if is_placeholder "${CONNECTOR_ENCRYPTION_KEY:-}"; then
  CONNECTOR_ENCRYPTION_KEY="$(read_or_create_secret tmp/dev-connector-key connector)"
fi
if is_placeholder "${LLM_GATEWAY_TOKEN:-}"; then
  LLM_GATEWAY_TOKEN="$(read_or_create_secret tmp/dev-llm-gateway-token token)"
fi
if is_placeholder "${SEEDE2E_PASSWORD:-}"; then
  SEEDE2E_PASSWORD="$(read_or_create_secret tmp/dev-owner-password password)"
fi
export CONNECTOR_ENCRYPTION_KEY LLM_GATEWAY_TOKEN SEEDE2E_PASSWORD
export SEEDE2E_EMAIL="${SEEDE2E_EMAIL:-owner@dev.local}"

# Grafana admin (issue #10): anonymous Admin is disabled, so `task dev` needs a
# real admin login. Generate a random dev-only password under tmp/ when the
# environment does not already provide one, then export it so the `task dev`
# compose-up inherits it (never a predictable admin/admin default).
if is_placeholder "${GF_SECURITY_ADMIN_PASSWORD:-}"; then
  GF_SECURITY_ADMIN_PASSWORD="$(read_or_create_secret tmp/dev-grafana-admin-password password)"
fi
export GF_SECURITY_ADMIN_USER="${GF_SECURITY_ADMIN_USER:-admin}"
export GF_SECURITY_ADMIN_PASSWORD

echo "Starting local infrastructure..."
task dev

echo "Preparing the local database (non-destructive, idempotent migrations + fixtures)..."
goose -dir services/core/migrations postgres "$DATABASE_URL" up
river migrate-up --database-url "$DATABASE_URL"
docker compose -f deploy/compose.dev.yml exec -T postgres \
  psql -U market_ops -d market_ops -v ON_ERROR_STOP=1 \
  <services/core/fixtures/dev_seed.sql >/dev/null
(
  cd services/core
  GOWORK=off go run ./cmd/seede2e
)

echo "Building the Go core..."
task go:build

llm_pid=""
core_pid=""
web_pid=""

cleanup() {
  local pid
  for pid in "$web_pid" "$core_pid" "$llm_pid"; do
    if [[ -n "$pid" ]]; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT
trap 'exit 130' INT TERM

wait_for_http() {
  local service_name="$1"
  local url="$2"
  local pid="$3"
  local log_file="$4"
  local method="${5:-GET}"
  local attempt

  for attempt in {1..60}; do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "task up: $service_name exited before becoming ready" >&2
      tail -n 80 "$log_file" >&2 || true
      return 1
    fi
    if curl -fsS -X "$method" "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done

  echo "task up: timed out after $attempt attempts waiting for $service_name at $url" >&2
  tail -n 80 "$log_file" >&2 || true
  return 1
}

echo "Starting LLM plane on :8100 (provider: $LLM_PROVIDER_KIND)..."
(
  cd services/llm
  env -u DATABASE_URL -u CONNECTOR_ENCRYPTION_KEY -u DK_API_BASE_URL \
    -u SEEDE2E_EMAIL -u SEEDE2E_PASSWORD \
    uv run uvicorn llm.asgi:app --app-dir src --host 127.0.0.1 --port 8100
) >tmp/up-llm.log 2>&1 &
llm_pid=$!
wait_for_http "LLM plane" "http://127.0.0.1:8100/healthz" "$llm_pid" tmp/up-llm.log

echo "Starting Go core on :8080..."
env -u SEEDE2E_EMAIL -u SEEDE2E_PASSWORD \
  services/core/bin/core >tmp/up-core.log 2>&1 &
core_pid=$!
wait_for_http "Go core" "http://127.0.0.1:8080/healthz" "$core_pid" tmp/up-core.log

echo "Starting Vite dev server on :5173..."
env -u DATABASE_URL -u CONNECTOR_ENCRYPTION_KEY -u LLM_GATEWAY_TOKEN \
  -u LLM_PROVIDER_API_KEY -u SEEDE2E_EMAIL -u SEEDE2E_PASSWORD \
  MARKET_OPS_DEV_OWNER_EMAIL="$SEEDE2E_EMAIL" \
  MARKET_OPS_DEV_OWNER_PASSWORD="$SEEDE2E_PASSWORD" \
  pnpm --filter @market-ops/web dev >tmp/up-web.log 2>&1 &
web_pid=$!
wait_for_http "Vite" "http://localhost:5173/" "$web_pid" tmp/up-web.log
wait_for_http "Vite gateway proxy" "http://localhost:5173/api/healthz" "$web_pid" tmp/up-web.log
wait_for_http \
  "Vite dev session bootstrap" \
  "http://localhost:5173/api/dev/session" \
  "$web_pid" \
  tmp/up-web.log \
  POST

echo
echo "market-ops is ready. Ctrl-C stops the application processes."
echo
echo "  Web SPA      http://localhost:5173"
echo "  Gateway      http://localhost:5173/api  (same-origin Vite proxy)"
echo "  Go core      http://localhost:8080"
echo "  LLM plane    http://localhost:8100       ($LLM_PROVIDER_KIND provider)"
echo "  Dev owner    $SEEDE2E_EMAIL"
echo "  Password     tmp/dev-owner-password      (mode 0600)"
echo "  Grafana      http://localhost:3000       (login: admin / tmp/dev-grafana-admin-password)"
echo "  Logs         tmp/up-{llm,core,web}.log"
echo

while :; do
  for process in \
    "LLM plane:$llm_pid:tmp/up-llm.log" \
    "Go core:$core_pid:tmp/up-core.log" \
    "Vite:$web_pid:tmp/up-web.log"; do
    service_name="${process%%:*}"
    remainder="${process#*:}"
    pid="${remainder%%:*}"
    log_file="${remainder#*:}"
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "task up: $service_name stopped unexpectedly" >&2
      tail -n 80 "$log_file" >&2 || true
      exit 1
    fi
  done
  sleep 2
done
