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
# Issue #19 second remediation: `passed` claimed with ABSENT evidence, and the
# casing bypass of the gate rule.
ERASED_EVIDENCE="$HERE/testdata/erased_evidence.md"
CASE_VARIANT="$HERE/testdata/case_variant.md"
# Issue #19 cycle-1: the two ways the evidence rule was itself bypassable.
HOP_LAUNDERED="$HERE/testdata/hop_laundered.md"
EMPTY_NOTE_GATE="$HERE/testdata/empty_note_gate.md"
GATE_SATISFIED="$HERE/testdata/gate_satisfied.md"
# Issue #19 cycle-2: placeholder evidence notes, and the registry-block
# delimiters that the evidence rule's boundary depends on.
ZWSP_NOTE_GATE="$HERE/testdata/zwsp_note_gate.md"
PLACEHOLDER_NOTE_GATE="$HERE/testdata/placeholder_note_gate.md"
PERSIAN_NOTE_GATE="$HERE/testdata/persian_note_gate.md"
REGISTRY_COLLAPSED="$HERE/testdata/registry_collapsed_block.md"
REGISTRY_TRUNCATED="$HERE/testdata/registry_truncated_block.md"
TRANSITIONS_COLLAPSED="$HERE/testdata/transitions_collapsed_block.md"
# Issue #19 cycle-3: the delimiter shapes reached by DELETING or REORDERING a
# marker rather than joining two, and the invisible-but-alphanumeric note.
REGISTRY_UNTERMINATED="$HERE/testdata/registry_unterminated_block.md"
TRANSITIONS_UNTERMINATED="$HERE/testdata/transitions_unterminated_block.md"
REGISTRY_OUT_OF_ORDER="$HERE/testdata/registry_out_of_order_block.md"
FILLER_NOTE_GATE="$HERE/testdata/filler_note_gate.md"
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

# --- Issue #19 (second remediation): evidence must be PRESENT, not just -------
# --- not-negative. The first remediation rejected `passed` only when a
# --- `pending-mandatory` GATE row was present to contradict it, so the
# --- enforcement could be bypassed by DELETING the evidence, or by respelling
# --- the status token. Both are the same defect class the issue names.

# 8. NEGATIVE: a step leaving an outstanding-verification state (`verify-pending`
#    / `blocked`) for `passed` with its GATE row and deferred bullet DELETED —
#    i.e. mandatory verification evidence simply ABSENT — MUST be rejected.
expect_exit "rejects passed with ABSENT evidence (gate row erased)" 1 \
  python3 "$VALIDATOR" --file "$ERASED_EVIDENCE"

# 9. NEGATIVE: the gate rule must key on the canonical status, not the raw
#     string — `Passed` is the same state as `passed` and MUST be rejected
#     identically when a pending-mandatory gate contradicts it.
expect_exit "rejects casing bypass (Passed + pending-mandatory)" 1 \
  python3 "$VALIDATOR" --file "$CASE_VARIANT"

# 10. NEGATIVE: the same ABSENT evidence reached through a longer LEGAL chain
#     (`verify-pending -> blocked -> in-progress -> passed`). Keying the evidence
#     rule on the immediate predecessor let this route around it; the rule keys
#     on the step's transition HISTORY, so it MUST be rejected.
expect_exit "rejects hop-chain laundering (verify-pending -> ... -> passed)" 1 \
  python3 "$VALIDATOR" --file "$HOP_LAUNDERED"

# 11. NEGATIVE: a `satisfied` GATE row whose evidence note is EMPTY — a state
#     token standing in for a record — MUST NOT satisfy the evidence rule.
expect_exit "rejects empty-note satisfied gate (token without a record)" 1 \
  python3 "$VALIDATOR" --file "$EMPTY_NOTE_GATE"

# 12. POSITIVE (anti-over-rejection): the legitimate exit from an outstanding
#     verification state — the gate flipped to `satisfied` with its evidence —
#     MUST be accepted. This is the shape S2 takes once its runtime Verify runs.
expect_exit "accepts verify-pending -> passed WITH a satisfied gate" 0 \
  python3 "$VALIDATOR" --file "$GATE_SATISFIED"

# --- Issue #19 (cycle-2): the note-content floor, and the registry boundary ---
# --- the evidence rule stands on. A non-whitespace test was not a content
# --- test: `str.strip()` removes Unicode whitespace but NOT format characters,
# --- so an invisible note passed it while rendering exactly like the empty note
# --- case 11 pins.

# 13. NEGATIVE: a `satisfied` note that is a single ZERO-WIDTH SPACE (U+200B) —
#     visually identical to an empty note — MUST be rejected like case 11.
expect_exit "rejects invisible-placeholder satisfied note (U+200B)" 1 \
  python3 "$VALIDATOR" --file "$ZWSP_NOTE_GATE"

# 14. NEGATIVE: the visible twin — a punctuation-only `satisfied` note ('-') —
#     MUST be rejected. It is non-empty and non-whitespace but records nothing.
expect_exit "rejects punctuation-only satisfied note ('-')" 1 \
  python3 "$VALIDATOR" --file "$PLACEHOLDER_NOTE_GATE"

# 15. POSITIVE (anti-over-rejection, localization boundary): a real evidence note
#     written in Persian — non-Latin script, ZWNJ inside words, Persian-Indic
#     digits — MUST be accepted. The content test is Unicode-aware on purpose;
#     an ASCII [0-9A-Za-z] test would reject this and force evidence into
#     English. Locale is data, never a reason to reject a record.
#     MEASURED (cycle-3): the fixture's S2 gate note carries no ASCII
#     alphanumeric character, so it exits 1 under an ASCII-predicate variant of
#     the validator and 0 here — the pin discriminates between the two
#     predicates. An earlier revision left the Latin word `healthy` in the note,
#     which BOTH predicates accepted, so the pin was inert and a future
#     hardening to ASCII would have kept this suite green.
expect_exit "accepts a legitimate Persian-script evidence note" 0 \
  python3 "$VALIDATOR" --file "$PERSIAN_NOTE_GATE"

# 16. NEGATIVE: BEGIN and END on ONE line must NOT leave the registry block open
#     for the rest of the document (which made every `GATE` row in ordinary prose
#     count as evidence). MUST be rejected.
expect_exit "rejects collapsed registry delimiter (BEGIN and END on one line)" 1 \
  python3 "$VALIDATOR" --file "$REGISTRY_COLLAPSED"

# 17. NEGATIVE: a duplicated END token (here smuggled in prose inside the block)
#     silently truncated the registry, hiding the `pending-mandatory` row that
#     contradicts S2's `passed`. A duplicated delimiter MUST be rejected.
expect_exit "rejects duplicated registry terminator (hidden gate row)" 1 \
  python3 "$VALIDATOR" --file "$REGISTRY_TRUNCATED"

# 18. NEGATIVE: the transition-log twin — a collapsed TXN delimiter let prose
#     `TXN` rows be replayed as logged transitions, so parity "held" on a history
#     that was never inside the machine-checked block. MUST be rejected.
expect_exit "rejects collapsed transition-log delimiter (prose TXN rows)" 1 \
  python3 "$VALIDATOR" --file "$TRANSITIONS_COLLAPSED"

# --- Issue #19 (cycle-3): the SAME out-of-block payload as cases 16-18, reached
# --- by DELETING or REORDERING a marker instead of joining two. A delimiter
# --- check that only counts duplicates ("at most once") accepted all three:
# --- each exits 0 under the pre-fix validator, with the injected out-of-block
# --- row proven load-bearing (removing it flips the file to exit 1).

# 19. NEGATIVE: a registry block whose END marker is simply ABSENT stays open to
#     EOF, so the injected out-of-block `GATE S2 | satisfied` row became S2's
#     unlock evidence. MUST be rejected.
expect_exit "rejects unterminated registry block (no END marker)" 1 \
  python3 "$VALIDATOR" --file "$REGISTRY_UNTERMINATED"

# 20. NEGATIVE: the transition-log twin — no TXN END marker, so prose TXN rows
#     were replayed as logged history and parity "held". MUST be rejected.
expect_exit "rejects unterminated transition-log block (no END marker)" 1 \
  python3 "$VALIDATOR" --file "$TRANSITIONS_UNTERMINATED"

# 21. NEGATIVE: both markers present exactly ONCE but END before BEGIN — marker
#     counts alone do not make a block well formed; the terminator closes nothing
#     and the opener runs to EOF. MUST be rejected.
expect_exit "rejects out-of-order registry delimiters (END before BEGIN)" 1 \
  python3 "$VALIDATOR" --file "$REGISTRY_OUT_OF_ORDER"

# 22. NEGATIVE: a `satisfied` note built only from the invisible Hangul fillers
#     U+115F/U+1160/U+3164/U+FFA0. They are Unicode category Lo, so `isalnum()`
#     is True for each and the note passed the content test, yet it renders blank
#     — indistinguishable in review from the empty note of case 11 and the
#     zero-width note of case 13, which are both rejected. MUST be rejected.
expect_exit "rejects invisible-letter-filler satisfied note (U+3164 et al)" 1 \
  python3 "$VALIDATOR" --file "$FILLER_NOTE_GATE"

# 23. The real, now-reconciled orchestration ledger MUST be accepted (parity
#    holds: every non-initial table state has a producing, legal transition).
expect_exit "accepts the real reconciled ledger" 0 \
  python3 "$VALIDATOR" --file "$LEDGER"

# 24. INPUT ERROR: an unreadable ledger (here: a directory) is neither a clean
#     run (0) nor a violation (1) — it MUST exit 2 with an actionable message,
#     never an unhandled traceback.
expect_exit "exits 2 on an unreadable ledger path (no traceback)" 2 \
  python3 "$VALIDATOR" --file "$HERE/testdata"

if [ "$fail" -ne 0 ]; then
  echo "ledger validator test: FAILED" >&2
  exit 1
fi
echo "ledger validator test: PASSED"
