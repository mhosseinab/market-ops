#!/usr/bin/env bash

# Keep dependency downloads and compiled objects across the integration suite's
# three destructive Compose lifecycles. CI may provide cache paths restored by
# actions/cache; local runs default to the repository's ignored .cache tree.
MARKET_OPS_COMPOSE_EXTRA_FILE="${MARKET_OPS_COMPOSE_EXTRA_FILE:-deploy/compose.test.cache.yml}"
MARKET_OPS_GO_CACHE_DIR="${MARKET_OPS_GO_CACHE_DIR:-$ROOT_DIR/.cache/integration/go}"
MARKET_OPS_UV_CACHE_DIR="${MARKET_OPS_UV_CACHE_DIR:-$ROOT_DIR/.cache/integration/uv}"

export MARKET_OPS_COMPOSE_EXTRA_FILE MARKET_OPS_GO_CACHE_DIR MARKET_OPS_UV_CACHE_DIR
mkdir -p "$MARKET_OPS_GO_CACHE_DIR/mod" "$MARKET_OPS_GO_CACHE_DIR/build" \
  "$MARKET_OPS_GO_CACHE_DIR/bin" "$MARKET_OPS_UV_CACHE_DIR"
