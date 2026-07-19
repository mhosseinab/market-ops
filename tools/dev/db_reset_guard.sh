#!/usr/bin/env bash
set -euo pipefail

# db_reset_guard.sh — fail-closed safety gate in front of the destructive
# `task db:reset` DROP DATABASE (issue #9).
#
# The guard is the PRODUCER of the safety decision; `task db:reset` is the real
# consumer and MUST call this script as its first command, aborting on a
# non-zero exit BEFORE any psql/DROP runs. Every reject path exits non-zero with
# an actionable message on stderr that names the failing safety condition and
# NEVER prints the DATABASE_URL, its credentials, or its query string. On accept
# the guard exits 0 and echoes only the parsed host + database name.
#
# Safety model (default = local/dev only):
#   * Allowed hosts:  localhost, 127.0.0.1, ::1, [::1]. An empty/unparseable
#     host (e.g. a bare local socket) is REJECTED by default.
#   * Allowed db names: `market_ops` and any `market_ops*` / `*_dev` dev name,
#     restricted to the [A-Za-z0-9_-] charset (no SQL metacharacters — the name
#     is interpolated into the destructive DROP DATABASE statement).
#   * Connection-target libpq query keywords (host, hostaddr, port, dbname,
#     service) are ALWAYS rejected: libpq honours them over the vetted authority
#     host, so they are a no-override bypass to a remote DROP. sslmode and other
#     non-connection params are permitted.
#   * Protected db names are ALWAYS rejected (even locally, even with override):
#     postgres, template0, template1, production, prod, main, master.
#   * Prod-like environments (APP_ENV/ENVIRONMENT/ENV in {prod, production,
#     staging}) are ALWAYS rejected.
#   * A non-local/non-allowlisted host may proceed ONLY when the deliberate,
#     high-friction override DB_RESET_ALLOW_NONLOCAL=1 is set — a variable
#     SEPARATE from DATABASE_URL. The override widens the HOST allowlist only;
#     it never relaxes protected-name or prod-env rejection.
#
# The guard never connects to a database; it only decides. It is unit-testable
# without the `task` runner: see tools/dev/db_reset_guard_test.sh.

OVERRIDE_VAR="DB_RESET_ALLOW_NONLOCAL"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  cat <<'HELP'
db_reset_guard.sh — fail-closed gate for the destructive `task db:reset`.

Reads DATABASE_URL from the environment, parses the host and target database
name, and exits 0 only when the target is an approved local/dev database.

Default allowlist (no override needed):
  hosts : localhost, 127.0.0.1, ::1, [::1]
  db    : market_ops, market_ops*, *_dev

Always rejected (even locally, even with the override):
  db    : postgres, template0, template1, production, prod, main, master
  db    : any name with characters outside [A-Za-z0-9_-]
  query : libpq connection-target keywords host/hostaddr/port/dbname/service
  env   : APP_ENV / ENVIRONMENT / ENV in {prod, production, staging}

Non-local target override (deliberate, high-friction, SEPARATE from DATABASE_URL):
  export DB_RESET_ALLOW_NONLOCAL=1
  # widens the HOST allowlist only; protected names and prod-like envs stay rejected.

The guard never prints DATABASE_URL, credentials, or the query string.
HELP
  exit 0
fi

reject() {
  # $1 = safety condition (no secrets). Exit non-zero, fail closed.
  echo "db:reset refused (fail-closed): $1" >&2
  exit 1
}

# --- Fail-closed: DATABASE_URL must be present. ------------------------------
if [[ -z "${DATABASE_URL:-}" ]]; then
  echo "DATABASE_URL is not set; export it before running db:reset." >&2
  echo "  form: DATABASE_URL='<scheme>://<user>:<redacted>@localhost:5432/market_ops?...'" >&2
  exit 1
fi

# --- Prod-like environment: rejected before any parsing/psql. ----------------
# Checked first and independently of the URL so a prod/staging context can never
# reset even a superficially-local database.
for env_name in APP_ENV ENVIRONMENT ENV; do
  env_val="${!env_name:-}"
  [[ -z "$env_val" ]] && continue
  case "$(printf '%s' "$env_val" | tr '[:upper:]' '[:lower:]')" in
    prod | production | staging)
      reject "environment ${env_name}='${env_val}' is prod-like; db:reset is only permitted in dev"
      ;;
  esac
done

# --- Parse host and db name from DATABASE_URL (same derivation as db:reset). --
# Strip scheme, then userinfo, then split host[:port] from the /path, dropping
# any ?query. We deliberately parse INSIDE the guard so the Taskfile aborts here
# before any psql call.
no_scheme="${DATABASE_URL#*://}"
authority="${no_scheme%%/*}"          # userinfo@host:port
path_and_query="${no_scheme#*/}"      # dbname?query  (or the whole thing if no '/')

# Drop userinfo (user[:password]@) — never surfaced anywhere.
hostport="${authority##*@}"

# Extract the db name: path segment up to the query string.
if [[ "$path_and_query" == "$no_scheme" ]]; then
  # No '/' after the authority ⇒ no database in the path.
  appdb=""
else
  appdb="${path_and_query%%\?*}"
fi

# Derive the host from host[:port], correctly handling bracketed IPv6.
if [[ "$hostport" == \[* ]]; then
  # [::1]:5432  or  [::1]
  host="${hostport%%]*}]"             # keep the closing bracket → "[::1]"
else
  host="${hostport%%:*}"              # strip :port for host:port
fi

if [[ -z "$appdb" ]]; then
  reject "could not parse a target database name from DATABASE_URL"
fi
if [[ -z "$host" ]]; then
  reject "could not parse a host from DATABASE_URL (empty/local-socket hosts are not allowed)"
fi

# --- Connection-target query keywords: ALWAYS rejected. ----------------------
# libpq honours connection keywords supplied in the URI query string
# (host, hostaddr, port, dbname, service), and they OVERRIDE the authority host
# this guard validated (e.g. `.../market_ops?host=db.prod.internal` connects to
# db.prod.internal, not localhost). The destructive db:reset re-attaches the
# query to its maintenance URL, so an unvetted connection-target keyword is a
# no-override bypass reaching a remote DROP. Fail closed on any of them, matched
# as real query keys (not substrings) and case-insensitively. Non-connection
# params such as sslmode remain allowed. The value is never printed.
if [[ "$DATABASE_URL" == *\?* ]]; then
  query="${DATABASE_URL#*\?}"
  # libpq/URI param separator is '&'. Iterate keys only.
  saved_ifs="$IFS"
  IFS='&'
  # shellcheck disable=SC2206  # deliberate word split on '&'
  params=($query)
  IFS="$saved_ifs"
  for param in "${params[@]}"; do
    [[ -z "$param" ]] && continue
    key="${param%%=*}"
    case "$(printf '%s' "$key" | tr '[:upper:]' '[:lower:]')" in
      host | hostaddr | port | dbname | service)
        reject "DATABASE_URL query string sets connection-target keyword '$(printf '%s' "$key" | tr '[:upper:]' '[:lower:]')', which libpq honours over the vetted host/database; remove it from DATABASE_URL (only non-connection params like sslmode are permitted)"
        ;;
    esac
  done
fi

# --- Target database name: strict charset (no SQL metacharacters). -----------
# The db name is interpolated into an unquoted `psql -c "DROP DATABASE ... "`
# in db:reset, so a name with SQL metacharacters is an injection into that
# destructive statement (e.g. market_ops";DROP DATABASE "production). Restrict
# to a conservative, PostgreSQL-safe identifier charset BEFORE any psql runs.
if [[ ! "$appdb" =~ ^[A-Za-z0-9_-]+$ ]]; then
  reject "target database name contains characters outside [A-Za-z0-9_-]; refusing to interpolate it into DROP DATABASE"
fi

# --- Protected db names: ALWAYS rejected (even locally, even with override). --
case "$(printf '%s' "$appdb" | tr '[:upper:]' '[:lower:]')" in
  postgres | template0 | template1 | production | prod | main | master)
    reject "target database '${appdb}' is a protected name and must never be reset"
    ;;
esac

# --- Host allowlist decision. -------------------------------------------------
host_is_local=0
case "$host" in
  localhost | 127.0.0.1 | ::1 | "[::1]")
    host_is_local=1
    ;;
esac

if [[ "$host_is_local" -ne 1 ]]; then
  if [[ "${!OVERRIDE_VAR:-}" != "1" ]]; then
    reject "host '${host}' is not in the local/dev allowlist and ${OVERRIDE_VAR} is not set (export ${OVERRIDE_VAR}=1 to deliberately target a non-local database)"
  fi
  # Override present: host allowed. Protected-name and prod-env checks above
  # have already run and are NOT relaxed by the override.
fi

# --- Dev database-name allowlist (applies to local hosts). -------------------
# The override widens the HOST only. A non-local target that passed via the
# override still had its protected-name check enforced; we accept its db name
# without the dev-pattern restriction because it was a deliberate operator
# choice. For local hosts we require a recognizable dev name to avoid resetting
# an unrelated local database that merely happens to live on localhost.
if [[ "$host_is_local" -eq 1 ]]; then
  case "$appdb" in
    market_ops | market_ops* | *_dev) ;;
    *)
      reject "database '${appdb}' on local host '${host}' is not an approved dev database name (expected market_ops, market_ops*, or *_dev)"
      ;;
  esac
fi

echo "db:reset guard: target approved (host='${host}', database='${appdb}')."
exit 0
