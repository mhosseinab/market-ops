#!/usr/bin/env bash
# Test harness for the orchestration-ledger verification-gate validator (issue #19).
#
# Enforces the never-cut ledger-integrity rule: a step may be recorded `passed`
# (and thereby satisfy a dependency gate) ONLY after every MANDATORY verification
# has a successful evidence record. A step whose exact Verify block is recorded as
# a pending/deferred MANDATORY gate must NOT be `passed`.
#
# It also enforces status-table parity (issue #20): the validator replays the
# machine-checked transition log, derives per-step state, and asserts it EXACTLY
# equals the status table — failing closed on an unlogged table change, an
# illegal transition, or log/table divergence.
#
# Both directions are required evidence:
#   RED  fixture (S2 `passed` + pending-mandatory gate) -> validator MUST reject.
#   GREEN fixture (S2 `verify-pending`)                 -> validator MUST accept.
#   PARITY negatives (unlogged / illegal / divergence)  -> validator MUST reject.
#   PARITY positives (parity-holds / reopened-regressed)-> validator MUST accept.
#   The real reconciled ledger                          -> validator MUST accept.
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
VALIDATOR="$HERE/validate_ledger.py"
INCONSISTENT="$HERE/testdata/inconsistent.md"
RECONCILED="$HERE/testdata/reconciled.md"
# Issue #20 transition-log ⇄ status-table parity fixtures.
PARITY_OK="$HERE/testdata/parity_ok.md"
PARITY_UNLOGGED="$HERE/testdata/parity_unlogged.md"
PARITY_ILLEGAL="$HERE/testdata/parity_illegal.md"
PARITY_DIVERGENCE="$HERE/testdata/parity_divergence.md"
PARITY_REOPENED="$HERE/testdata/parity_reopened.md"
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

# --- Issue #20: transition-log ⇄ status-table PARITY (fail-closed) ---------
# The parity replay derives per-step state from the ordered transition log and
# asserts it EXACTLY equals the status table, handling blocked / in-progress /
# passed / verify-pending / reopened / regressed.

# 3. NEGATIVE: a table state with no producing transition (the issue-#20 bug —
#    a silent table edit) MUST be rejected.
expect_exit "rejects unlogged table change (passed with no transition)" 1 \
  python3 "$VALIDATOR" --file "$PARITY_UNLOGGED"

# 4. NEGATIVE: an illegal transition the state machine forbids MUST be rejected.
expect_exit "rejects illegal transition (passed -> in_progress)" 1 \
  python3 "$VALIDATOR" --file "$PARITY_ILLEGAL"

# 5. NEGATIVE: log/table divergence (replay derives a different current state)
#    MUST be rejected.
expect_exit "rejects log/table divergence (derives in_progress, table passed)" 1 \
  python3 "$VALIDATOR" --file "$PARITY_DIVERGENCE"

# 6. POSITIVE: the parity-holds base fixture MUST be accepted.
expect_exit "accepts parity-holds fixture" 0 \
  python3 "$VALIDATOR" --file "$PARITY_OK"

# 7. POSITIVE: reopened/regressed cycles that legally re-derive the table state
#    MUST be accepted.
expect_exit "accepts reopened/regressed cycles" 0 \
  python3 "$VALIDATOR" --file "$PARITY_REOPENED"

# 8. The real, now-reconciled orchestration ledger MUST be accepted (parity
#    holds: every non-initial table state has a producing, legal transition).
expect_exit "accepts the real reconciled ledger" 0 \
  python3 "$VALIDATOR" --file "$LEDGER"

if [ "$fail" -ne 0 ]; then
  echo "ledger validator test: FAILED" >&2
  exit 1
fi
echo "ledger validator test: PASSED"
