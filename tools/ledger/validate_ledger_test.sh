#!/usr/bin/env bash
# Test harness for the orchestration-ledger verification-gate validator (issue #19).
#
# Enforces the never-cut ledger-integrity rule: a step may be recorded `passed`
# (and thereby satisfy a dependency gate) ONLY after every MANDATORY verification
# has a successful evidence record. A step whose exact Verify block is recorded as
# a pending/deferred MANDATORY gate must NOT be `passed`.
#
# Both directions are required evidence:
#   RED  fixture (S2 `passed` + pending-mandatory gate) -> validator MUST reject.
#   GREEN fixture (S2 `verify-pending`)                 -> validator MUST accept.
#   The real reconciled ledger                          -> validator MUST accept.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
VALIDATOR="$HERE/validate_ledger.py"
INCONSISTENT="$HERE/testdata/inconsistent.md"
RECONCILED="$HERE/testdata/reconciled.md"
LEDGER="$REPO_ROOT/docs/implementation/dk-p0-progress.md"

fail=0

expect_exit() {
  # $1 = human label, $2 = expected exit code, $3.. = command
  local label="$1"; local want="$2"; shift 2
  "$@" >/dev/null 2>&1
  local got=$?
  if [ "$got" -ne "$want" ]; then
    echo "FAIL: $label — expected exit $want, got $got" >&2
    fail=1
  else
    echo "ok: $label (exit $got)"
  fi
}

# 1. NEGATIVE (first-class): the inconsistent fixture is the current pre-fix state.
#    A `passed` step with a pending MANDATORY gate MUST be rejected.
expect_exit "rejects inconsistent fixture (S2 passed + pending-mandatory)" 1 \
  python3 "$VALIDATOR" --file "$INCONSISTENT"

# 2. The reconciled fixture (S2 -> verify-pending) MUST be accepted.
expect_exit "accepts reconciled fixture (S2 verify-pending)" 0 \
  python3 "$VALIDATOR" --file "$RECONCILED"

# 3. The real, now-reconciled orchestration ledger MUST be accepted.
expect_exit "accepts the real reconciled ledger" 0 \
  python3 "$VALIDATOR" --file "$LEDGER"

if [ "$fail" -ne 0 ]; then
  echo "ledger validator test: FAILED" >&2
  exit 1
fi
echo "ledger validator test: PASSED"
