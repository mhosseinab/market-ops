# Fixture: registry END before BEGIN — both present exactly once (issue #19, cycle-3)
#
# Marker COUNTS alone do not make a block well formed. Here each marker appears
# on exactly one line, so an "at most once" check is satisfied, but the END line
# precedes the BEGIN line: the terminator closes nothing, the BEGIN opens a block
# that never closes, and every `GATE` row after it runs to EOF as a registry row.
#
# The injected `GATE S2 | satisfied | ...` row is load-bearing: remove it and the
# evidence rule rejects the file. The delimiters must be required exactly once
# each AND in order.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) evidence injected after an out-of-order registry BEGIN |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind an out-of-order GATE row | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

<!-- LEDGER-VERIFICATION-GATES:BEGIN

## Notes

Everything below this line is document body, not registry.

GATE S2 | satisfied | (injected after an out-of-order BEGIN) claims the runtime Verify ran
GATE S6 | satisfied | (injected after an out-of-order BEGIN) CI green

> Status values: pending | in_progress | passed | verify-pending | blocked.
