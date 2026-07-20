# Fixture: log/table divergence (issue #20 negative)
#
# S1's transition log ends at `in_progress` but the status table says `passed`.
# The replay-derived state and the table describe DIFFERENT current states. A
# conforming validator MUST reject this (fail closed).

<!-- LEDGER-VERIFICATION-GATES:BEGIN
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Alpha | passed | 1 | dk-p0/S1 | aaa1111 | table claims passed |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> in_progress | dispatched | aaa1111
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked | reopened | regressed.
