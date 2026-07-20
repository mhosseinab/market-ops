# Fixture: illegal transition (issue #20 negative)
#
# S1 is logged pending -> passed -> in_progress. `passed -> in_progress` is a
# forbidden edge: a `passed` step can only revert via `reopened`/`regressed`,
# never silently back to `in_progress`. The final derived state (in_progress)
# equals the table, so the ONLY violation is the illegal edge. A conforming
# validator MUST reject this (fail closed).

<!-- LEDGER-VERIFICATION-GATES:BEGIN
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Alpha | in_progress | 2 | dk-p0/S1 | aaa1111 | un-passed without a reopen marker |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | single-cycle pass | aaa1111
TXN S1 | passed -> in_progress | silently un-passed | aaa1111
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked | reopened | regressed.
