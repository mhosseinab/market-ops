#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ROOT_DIR="$repo_root"

unset MARKET_OPS_COMPOSE_EXTRA_FILE MARKET_OPS_GO_CACHE_DIR MARKET_OPS_UV_CACHE_DIR
# shellcheck source=tools/integration/configure_cache.sh
source "$repo_root/tools/integration/configure_cache.sh"

test "$MARKET_OPS_COMPOSE_EXTRA_FILE" = "deploy/compose.test.cache.yml"
test "$MARKET_OPS_GO_CACHE_DIR" = "$repo_root/.cache/integration/go"
test "$MARKET_OPS_UV_CACHE_DIR" = "$repo_root/.cache/integration/uv"
test -d "$MARKET_OPS_GO_CACHE_DIR/mod"
test -d "$MARKET_OPS_GO_CACHE_DIR/build"
test -d "$MARKET_OPS_UV_CACHE_DIR"

MARKET_OPS_COMPOSE_EXTRA_FILE=custom.yml
MARKET_OPS_GO_CACHE_DIR=/tmp/custom-go-cache
MARKET_OPS_UV_CACHE_DIR=/tmp/custom-uv-cache
source "$repo_root/tools/integration/configure_cache.sh"

test "$MARKET_OPS_COMPOSE_EXTRA_FILE" = custom.yml
test "$MARKET_OPS_GO_CACHE_DIR" = /tmp/custom-go-cache
test "$MARKET_OPS_UV_CACHE_DIR" = /tmp/custom-uv-cache

echo "cache_config_test: local defaults and caller overrides passed"
