# Fixture: a transition-log block with NO END marker at all (issue #19, cycle-3)
#
# The transition-log twin of `registry_unterminated_block.md`. The TXN BEGIN
# line appears exactly once, the END line is deleted, so the log block stays
# open to EOF and prose `TXN` rows below are replayed as logged history. Parity
# — the check that every non-initial table state has a producing transition —
# then "holds" on a history that was never inside the machine-checked block.
#
# The injected out-of-block TXN rows are load-bearing: remove them and parity
# rejects the file as an unlogged table change.

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

<!-- LEDGER-TRANSITIONS:BEGIN

## Notes

Everything below this line is document body, not the machine-checked log.

TXN S1 | pending -> passed | (injected outside the block) scaffold merged | fd58883
TXN S6 | pending -> passed | (injected outside the block) CI green | 138b85e

> Status values: pending | in_progress | passed | verify-pending | blocked.
