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
# the guard exits 0 and echoes only the parsed host(s) + database name.
#
# Safety model (default = local/dev only):
#   * Allowed hosts:  localhost, 127.0.0.1, ::1, [::1]. libpq accepts a
#     COMMA-SEPARATED multi-host authority and fails over between entries, and
#     db:reset keeps the full authority in its maintenance URL, so the guard
#     validates EVERY host entry: ALL must be local (or the override set). An
#     empty/unparseable host entry (e.g. a bare local socket, or a stray double
#     comma) is REJECTED by default (fail closed).
#   * Allowed db names: `market_ops` and any `market_ops*` / `*_dev` dev name,
#     restricted to the [A-Za-z0-9_-] charset (no SQL metacharacters — the name
#     is interpolated into the destructive DROP DATABASE statement).
#   * Query string is fail-closed: any percent-encoding ('%') is rejected (libpq
#     decodes it into connection keywords the guard cannot vet — e.g. `%68ost`→
#     host), and any query key outside the strict connection-target-INERT
#     allowlist {sslmode} is rejected. libpq honours connection keywords over the
#     vetted authority host, so an unvetted key is a no-override bypass to a
#     remote DROP.
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
  query : any percent-encoding ('%'), or any key other than sslmode
          (libpq decodes/honours connection keywords over the vetted host)
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

# Derive the host from a single `host[:port]` authority entry, correctly handling
# bracketed IPv6 (`[::1]:5432` / `[::1]`). Prints the host (brackets kept for
# IPv6). Never prints ports, credentials, or the query string.
derive_host_from_entry() {
  local entry="$1"
  if [[ "$entry" == \[* ]]; then
    # [::1]:5432  or  [::1]  → keep the closing bracket → "[::1]"
    printf '%s' "${entry%%]*}]"
  else
    printf '%s' "${entry%%:*}"          # strip :port for host:port
  fi
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

# --- Parse host(s) and db name from DATABASE_URL (same derivation as db:reset). -
# Strip scheme, then userinfo, then split the authority from the /path, dropping
# any ?query. We deliberately parse INSIDE the guard so the Taskfile aborts here
# before any psql call.
#
# libpq accepts a COMMA-SEPARATED list of `host[:port]` entries in the authority
# and FAILS OVER between them at connection time. `task db:reset` keeps the FULL
# comma-separated authority in its maintenance URL (`server`/`maint_url`), so
# psql/libpq may connect to ANY entry — not just the first. The guard therefore
# validates EVERY host entry in the list: the effective host set it validates is
# exactly the one the consumer hands to psql. Validating only the first host
# would let a remote entry hidden behind a local first host reach a remote DROP.
no_scheme="${DATABASE_URL#*://}"
authority="${no_scheme%%/*}"          # userinfo@host[:port][,host[:port]...]
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

if [[ -z "$appdb" ]]; then
  reject "could not parse a target database name from DATABASE_URL"
fi

# Fail closed on an ambiguous multi-host list BEFORE splitting: a leading,
# trailing, or doubled comma implies an empty host entry whose intent cannot be
# vetted (same policy as a single empty host). An entirely-empty authority is
# likewise rejected. Only host names are ever surfaced — never the URL/creds.
if [[ -z "$hostport" || "$hostport" == ,* || "$hostport" == *, || "$hostport" == *,,* ]]; then
  reject "DATABASE_URL authority has an empty host entry (empty/local-socket hosts are not allowed)"
fi

# Split the full authority on ',' into host[:port] entries, preserving any empty
# fields so a stray empty entry cannot be silently collapsed away.
saved_ifs="$IFS"
IFS=',' read -ra host_entries <<<"$hostport"
IFS="$saved_ifs"

# Validate every entry. Track whether ANY host is non-local (order-independent)
# and remember the FIRST offending host for a single-host reject message. The
# accept echo lists hostnames only (comma-joined) — never scheme/creds/query.
host_list=""
any_nonlocal=0
first_nonlocal=""
for entry in "${host_entries[@]}"; do
  if [[ -z "$entry" ]]; then
    reject "DATABASE_URL authority has an empty host entry (empty/local-socket hosts are not allowed)"
  fi
  entry_host="$(derive_host_from_entry "$entry")"
  if [[ -z "$entry_host" ]]; then
    reject "could not parse a host from DATABASE_URL (empty/local-socket hosts are not allowed)"
  fi
  if [[ -z "$host_list" ]]; then
    host_list="$entry_host"
  else
    host_list="${host_list},${entry_host}"
  fi
  case "$entry_host" in
    localhost | 127.0.0.1 | ::1 | "[::1]") ;;
    *)
      any_nonlocal=1
      [[ -z "$first_nonlocal" ]] && first_nonlocal="$entry_host"
      ;;
  esac
done

# --- Query string: strict connection-target-INERT allowlist + no encoding. ---
# libpq honours connection keywords supplied in the URI query string
# (host, hostaddr, port, dbname, service, and aliases), and they OVERRIDE the
# authority host this guard validated (e.g. `.../market_ops?host=db.prod.internal`
# connects to db.prod.internal, not localhost). The destructive db:reset
# re-attaches the query to its maintenance URL, so ANY unvetted key is a
# no-override bypass reaching a remote DROP.
#
# A denylist of raw keyword strings is the WRONG shape: libpq PERCENT-DECODES
# query keys AFTER this guard would compare them, so `%68ost`→host, `%70ort`→port,
# `%73ervice`→service slip past a raw-string denylist and re-target the DROP. We
# therefore FAIL CLOSED two ways, together closing the entire encoding-trick class:
#   1. Reject if the query string contains ANY literal '%' (percent-encoding).
#      No decoded keyword can then reach libpq, whatever its encoded form.
#   2. Reject if ANY query key is outside a strict allowlist of params proven not
#      to affect the connection target. Only `sslmode` is allowed — the sole
#      query param used by the CI and dev (`tools/dev/up.sh`) URLs. If a future
#      local flow needs another param, add it here ONLY after confirming it is
#      connection-target-inert; when unsure, do not add it (fail closed).
# Keys are compared case-insensitively. The value is never printed.
if [[ "$DATABASE_URL" == *\?* ]]; then
  query="${DATABASE_URL#*\?}"
  if [[ "$query" == *%* ]]; then
    reject "DATABASE_URL query string contains percent-encoding, which libpq decodes into connection keywords the guard cannot vet; remove it (only a plain 'sslmode' param is permitted)"
  fi
  # libpq/URI param separator is '&'. Iterate keys only.
  saved_ifs="$IFS"
  IFS='&'
  # shellcheck disable=SC2206  # deliberate word split on '&'
  params=($query)
  IFS="$saved_ifs"
  for param in "${params[@]}"; do
    [[ -z "$param" ]] && continue
    key="$(printf '%s' "${param%%=*}" | tr '[:upper:]' '[:lower:]')"
    case "$key" in
      sslmode)
        ;;
      *)
        reject "DATABASE_URL query key '${key}' is not in the connection-target-inert allowlist {sslmode}; libpq may honour it over the vetted host/database, so it is refused (remove all query params except sslmode)"
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

# --- Host allowlist decision (whole multi-host list). ------------------------
# If ANY host in the list is non-local, the deliberate high-friction override is
# required — a remote entry anywhere in the failover list can reach a remote
# DROP. The override widens the HOST set ONLY; protected-name and prod-env checks
# above have already run and are NOT relaxed by it.
if [[ "$any_nonlocal" -eq 1 ]]; then
  if [[ "${!OVERRIDE_VAR:-}" != "1" ]]; then
    reject "host '${first_nonlocal}' is not in the local/dev allowlist and ${OVERRIDE_VAR} is not set (export ${OVERRIDE_VAR}=1 to deliberately target a non-local database)"
  fi
fi

# --- Dev database-name allowlist (applies when ALL hosts are local). ----------
# The override widens the HOST set only. A list containing a non-local target
# that passed via the override still had its protected-name check enforced; we
# accept its db name without the dev-pattern restriction because it was a
# deliberate operator choice. When every host is local we require a recognizable
# dev name to avoid resetting an unrelated local database that merely happens to
# live on a local host.
if [[ "$any_nonlocal" -ne 1 ]]; then
  case "$appdb" in
    market_ops | market_ops* | *_dev) ;;
    *)
      reject "database '${appdb}' on local host(s) '${host_list}' is not an approved dev database name (expected market_ops, market_ops*, or *_dev)"
      ;;
  esac
fi

echo "db:reset guard: target approved (host='${host_list}', database='${appdb}')."
exit 0
