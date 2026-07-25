# Fixture: BEGIN and END on ONE line leaves the registry open forever (issue #19, cycle-2)
#
# The registry parser checked the BEGIN marker before the END marker and
# `continue`d, so a line carrying BOTH markers opened the block and never closed
# it. Every `GATE` row in the rest of the document body — ordinary prose, a
# review comment, anything — was then honoured as a registry row.
#
# That defeats the boundary the evidence rule depends on: the `satisfied` row
# below sits in plain prose, outside any registry block, yet a fail-open parser
# accepts it as the record that unlocks S2's `verify-pending -> passed`.
#
# A conforming validator MUST NOT treat a collapsed delimiter as an open block.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN LEDGER-VERIFICATION-GATES:END -->

## Notes

Some ordinary prose. Everything below this line is document body, not registry.

GATE S2 | satisfied | (injected outside the block) claims the runtime Verify ran
GATE S6 | satisfied | (injected outside the block) CI green

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) evidence injected outside a collapsed registry block |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind an out-of-block GATE row | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
