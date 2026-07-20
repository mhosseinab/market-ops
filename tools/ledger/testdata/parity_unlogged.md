# Fixture: unlogged table change (issue #20 negative)
#
# S3 shows `passed` in the status table but NO transition-log entry produces it.
# This is the exact issue-#20 bug: a silent table edit masquerading as a valid
# transition. A conforming validator MUST reject this (fail closed).

<!-- LEDGER-VERIFICATION-GATES:BEGIN
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Alpha | passed | 1 | dk-p0/S1 | aaa1111 | single-cycle pass |
| S3 | Charlie | passed | 1 | dk-p0/S3 | ccc3333 | silently marked passed with no logged transition |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | single-cycle pass | aaa1111
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked | reopened | regressed.
