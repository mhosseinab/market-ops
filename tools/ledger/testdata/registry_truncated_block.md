# Fixture: a stray END token TRUNCATES the registry and hides a gate (issue #19, cycle-2)
#
# The parser closed the block on any line CONTAINING the END token, so a line of
# prose carrying that token silently truncated the registry. Every row after it
# — here, the `pending-mandatory` row that contradicts S2's `passed` — was never
# seen, and the ledger validated clean while the contradiction sat in the file.
#
# The registry delimiters are structure, not prose: each must appear exactly
# once. A conforming validator MUST fail closed on a duplicated delimiter rather
# than silently reading a shorter registry than the one written.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
(prose smuggling the terminator: LEDGER-VERIFICATION-GATES:END)
GATE S2 | pending-mandatory | runtime Verify (Docker boot + PostgreSQL 18 assertion) never executed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) contradicting gate row hidden behind a stray terminator |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> passed | (bypass) merged without the runtime Verify | ee97605
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
