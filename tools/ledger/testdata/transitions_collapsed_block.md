# Fixture: the transition-log twin of `registry_collapsed_block.md` (issue #19, cycle-2)
#
# The transition-log scan had the same BEGIN-before-END fail-open as the gate
# registry, so a collapsed delimiter opened the block forever and every `TXN`
# row in ordinary prose was replayed as a real logged transition. Parity — the
# check that every non-initial table state has a producing transition — then
# accepted a history that was never recorded inside the machine-checked block.
#
# The guard is applied to BOTH blocks; this pins the transition-log half.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN LEDGER-TRANSITIONS:END -->

## Notes

Everything below this line is document body, not the machine-checked log.

TXN S1 | pending -> passed | (injected outside the block) scaffold merged | fd58883
TXN S6 | pending -> passed | (injected outside the block) CI green | 138b85e

> Status values: pending | in_progress | passed | verify-pending | blocked.
