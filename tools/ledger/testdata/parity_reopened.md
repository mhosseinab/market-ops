# Fixture: reopened / regressed handling (issue #20 positive)
#
# S1 is reopened after passing (a regression is found), reworked, and re-passed;
# S2 regresses and is re-passed. Both replay chains legally derive `passed`,
# matching the table. A conforming validator MUST accept this — the state
# machine handles `reopened` and `regressed` cycles.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Alpha | passed | 2 | dk-p0/S1 | aaa1111 | reopened then re-passed |
| S2 | Bravo | passed | 2 | dk-p0/S2 | bbb2222 | regressed then re-passed |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | first pass | aaa1111
TXN S1 | passed -> reopened | downstream regression surfaced | aaa1111
TXN S1 | reopened -> in_progress | rework dispatched | aaa1111
TXN S1 | in_progress -> passed | re-verified, dual review green | aaa1111
TXN S2 | pending -> passed | first pass | bbb2222
TXN S2 | passed -> regressed | metric regressed post-merge | bbb2222
TXN S2 | regressed -> passed | fix-forward re-verified | bbb2222
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked | reopened | regressed.
