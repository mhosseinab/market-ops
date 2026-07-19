#!/usr/bin/env bash
# Resolve the dev Grafana admin password (issue #10).
#
# Prints the password to stdout. If GF_SECURITY_ADMIN_PASSWORD is already set in
# the environment, that value is used verbatim. Otherwise a random dev-only
# password is read-or-created under tmp/dev-grafana-admin-password (mode 0600),
# mirroring the secret pattern in tools/dev/up.sh. This keeps a clean checkout of
# `task dev` runnable with zero manual env setup while never baking a predictable
# password (e.g. admin/admin) into deploy/compose.dev.yml.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

if [[ -n "${GF_SECURITY_ADMIN_PASSWORD:-}" ]]; then
  printf '%s' "$GF_SECURITY_ADMIN_PASSWORD"
  exit 0
fi

secret_path="tmp/dev-grafana-admin-password"
mkdir -p tmp
chmod 700 tmp 2>/dev/null || true

if [[ -s "$secret_path" ]]; then
  IFS= read -r value <"$secret_path"
else
  value="$(openssl rand -base64 24 | tr -d '\n')"
  umask 077
  printf '%s\n' "$value" >"$secret_path"
  chmod 600 "$secret_path"
fi

printf '%s' "$value"
