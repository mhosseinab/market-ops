# Fixture: a registry block with NO END marker at all (issue #19, cycle-3)
#
# `registry_collapsed_block.md` pins the JOINED-marker shape. This is the same
# payload reached by DELETING one marker instead of joining two: the BEGIN line
# is present exactly once, the END line is absent, so the parser sets
# `inside = True` and never clears it — the block stays open to EOF and every
# `GATE` row in ordinary prose below is honoured as a registry row.
#
# The injected out-of-block `GATE S2 | satisfied | ...` row is load-bearing:
# remove it and the file is rejected by the evidence rule. A validator that only
# counts duplicate markers ("at most once") accepts this file; the delimiters
# must be required EXACTLY once each, BEGIN before END.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN

## Notes

Some ordinary prose. Everything below this line is document body, not registry.

GATE S2 | satisfied | (injected outside the block) claims the runtime Verify ran
GATE S6 | satisfied | (injected outside the block) CI green

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) evidence injected into an unterminated registry block |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind an out-of-block GATE row | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
