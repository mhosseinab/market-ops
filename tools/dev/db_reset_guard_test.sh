#!/usr/bin/env bash
set -euo pipefail

# Table-driven matrix for tools/dev/db_reset_guard.sh — the fail-closed safety
# gate in front of the destructive `task db:reset` DROP DATABASE. Runs entirely
# offline: a `psql` STUB on PATH records (via a marker file) whether the guard
# ever reached a psql invocation, so we can prove REJECT cases abort BEFORE any
# database call. No real DB is ever touched.

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
guard="$repo_root/tools/dev/db_reset_guard.sh"

if [[ ! -f "$guard" ]]; then
  echo "db_reset_guard_test: guard script not found at $guard" >&2
  exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# psql stub: writes a marker so the test can assert whether the guard would have
# reached a real psql call. The guard must NEVER invoke psql on a REJECT path.
stub_dir="$work/bin"
mkdir -p "$stub_dir"
marker="$work/psql_invoked"
cat >"$stub_dir/psql" <<STUB
#!/usr/bin/env bash
echo "STUB-PSQL-CALLED" >>"$marker"
exit 0
STUB
chmod +x "$stub_dir/psql"

failures=0

# run_case NAME EXPECT_EXIT EXPECT_PSQL MUST_MATCH_REGEX ENV_ASSIGNMENTS...
# EXPECT_PSQL is "yes" (marker allowed to exist) or "no" (marker must NOT exist).
# MUST_MATCH_REGEX asserts the combined stdout+stderr names the safety
# condition; pass "" to skip. Env assignments are KEY=VALUE strings; a bare
# "UNSET:KEY" is a no-op (the minimal base env simply omits it).
run_case() {
  local name="$1" expect_exit="$2" expect_psql="$3" must_match="$4"
  shift 4

  rm -f "$marker"

  # Build an explicit, controlled environment. Start from a minimal base so an
  # ambient DATABASE_URL / APP_ENV from the caller can never leak into a case.
  local -a env_args=(
    "PATH=$stub_dir:$PATH"
    "HOME=${HOME:-/tmp}"
  )
  local kv
  for kv in "$@"; do
    if [[ "$kv" == UNSET:* ]]; then
      continue
    fi
    env_args+=("$kv")
  done

  local out rc
  set +e
  out="$(env -i "${env_args[@]}" bash "$guard" 2>&1)"
  rc=$?
  set -e

  local ok=1

  if [[ "$expect_exit" -eq 0 ]]; then
    if [[ "$rc" -ne 0 ]]; then
      echo "FAIL [$name]: expected exit 0, got $rc" >&2
      echo "---- output ----" >&2
      echo "$out" >&2
      ok=0
    fi
  else
    if [[ "$rc" -eq 0 ]]; then
      echo "FAIL [$name]: expected non-zero exit, got 0" >&2
      echo "---- output ----" >&2
      echo "$out" >&2
      ok=0
    fi
  fi

  if [[ "$expect_psql" == "no" && -f "$marker" ]]; then
    echo "FAIL [$name]: psql stub WAS invoked on a reject path (marker present)" >&2
    ok=0
  fi

  if [[ -n "$must_match" ]] && ! grep -Eq "$must_match" <<<"$out"; then
    echo "FAIL [$name]: output did not match /$must_match/" >&2
    echo "---- output ----" >&2
    echo "$out" >&2
    ok=0
  fi

  # No case may ever leak the password or a URL/query fragment. The parsed host
  # alone (e.g. db.prod.internal) is allowed; scheme, credentials, or query
  # strings are not.
  if grep -Eq "sslmode=disable|market_ops:market_ops|supersecret|postgres://" <<<"$out"; then
    echo "FAIL [$name]: output leaked a URL/credential fragment" >&2
    echo "---- output ----" >&2
    echo "$out" >&2
    ok=0
  fi

  if [[ "$ok" -eq 1 ]]; then
    echo "ok   [$name]"
  else
    failures=$((failures + 1))
  fi
}

CI_URL="postgres://market_ops:market_ops@localhost:5432/market_ops?sslmode=disable"

# ACCEPT: the exact CI shape (host localhost + db market_ops), no override.
run_case "accept-ci-localhost-market_ops" 0 yes "" \
  "DATABASE_URL=$CI_URL"

# ACCEPT: 127.0.0.1 host + dev-suffixed db name.
run_case "accept-127-dev-db" 0 yes "" \
  "DATABASE_URL=postgres://market_ops:market_ops@127.0.0.1:5432/market_ops_dev?sslmode=disable"

# REJECT: remote host, no override — psql must never be reached.
run_case "reject-remote-no-override" 1 no "not in the .*allowlist|DB_RESET_ALLOW_NONLOCAL" \
  "DATABASE_URL=postgres://market_ops:supersecret@db.prod.internal:5432/market_ops?sslmode=disable"

# REJECT: protected db name `postgres` on localhost.
run_case "reject-protected-postgres" 1 no "protected" \
  "DATABASE_URL=postgres://market_ops:market_ops@localhost:5432/postgres?sslmode=disable"

# REJECT: protected db name `production` on localhost.
run_case "reject-protected-production" 1 no "protected" \
  "DATABASE_URL=postgres://market_ops:market_ops@localhost:5432/production?sslmode=disable"

# REJECT: prod-like environment even with a fully-local URL.
run_case "reject-prod-env" 1 no "APP_ENV|ENVIRONMENT|prod" \
  "DATABASE_URL=$CI_URL" "APP_ENV=production"

# REJECT: missing override on a non-local target — assert the override-absence copy.
run_case "reject-missing-override-message" 1 no "DB_RESET_ALLOW_NONLOCAL" \
  "DATABASE_URL=postgres://market_ops:supersecret@db.prod.internal:5432/market_ops?sslmode=disable"

# REJECT: DATABASE_URL unset is a fail-closed condition owned by the guard.
run_case "reject-missing-database-url" 1 no "DATABASE_URL" \
  "UNSET:DATABASE_URL"

# ACCEPT (guarded): remote host WITH the deliberate high-friction override,
# non-protected db, non-prod env.
run_case "accept-remote-with-override" 0 yes "" \
  "DATABASE_URL=postgres://market_ops:supersecret@db.staging-host.internal:5432/market_ops?sslmode=disable" \
  "DB_RESET_ALLOW_NONLOCAL=1"

# REJECT: even WITH the override, a protected name stays rejected (override
# widens host only, never protected-name).
run_case "reject-override-still-blocks-protected" 1 no "protected" \
  "DATABASE_URL=postgres://market_ops:supersecret@db.staging-host.internal:5432/postgres?sslmode=disable" \
  "DB_RESET_ALLOW_NONLOCAL=1"

# REJECT: even WITH the override, a prod-like env stays rejected.
run_case "reject-override-still-blocks-prod-env" 1 no "APP_ENV|ENVIRONMENT|prod" \
  "DATABASE_URL=postgres://market_ops:supersecret@db.staging-host.internal:5432/market_ops?sslmode=disable" \
  "DB_RESET_ALLOW_NONLOCAL=1" "ENVIRONMENT=staging"

if [[ "$failures" -ne 0 ]]; then
  echo "db_reset_guard_test: $failures case(s) failed" >&2
  exit 1
fi

echo "db_reset_guard_test: all guard safety cases passed"
