# Fixture: parity holds (issue #20 positive base)
#
# Every non-initial status-table state has a producing transition, the replay
# derives exactly the table state, and every transition is legal. A `pending`
# step (S3) needs no transition (pending is the initial state). A conforming
# validator MUST accept this.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Alpha | passed | 1 | dk-p0/S1 | aaa1111 | single-cycle pass |
| S2 | Bravo | verify-pending | 1 | dk-p0/S2 | bbb2222 | merged, runtime verify gated |
| S3 | Charlie | pending | 0 | — | — | not started |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | single-cycle pass, dual review green | aaa1111
TXN S2 | pending -> verify-pending | merged; runtime Verify gated to unrestricted host | bbb2222
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked | reopened | regressed.
