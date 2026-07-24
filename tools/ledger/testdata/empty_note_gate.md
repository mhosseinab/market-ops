# Fixture: a `satisfied` GATE row with an EMPTY evidence note (issue #19, cycle-1)
#
# The evidence rule demands a `satisfied` GATE row before a step that declared an
# outstanding mandatory verification may reach `passed`. If the rule only counts
# the STATE TOKEN, the cheapest bypass is to write the token with no record
# behind it: `GATE S2 | satisfied |` with an empty note asserts that a runtime
# Verify ran while carrying zero evidence that it did — exactly the "absent
# evidence" condition the rule exists to reject, laundered through the rule
# itself.
#
# The gate row IS the evidence record; a row with no note is not a record. A
# conforming validator MUST reject this as it rejects `erased_evidence.md`.
#
# (The real ledger's `satisfied` rows — S6 and S32 — both carry substantive
# evidence notes, so this check costs the real ledger nothing.)

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | satisfied |
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) empty-note `satisfied` gate standing in for evidence |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind a contentless gate row | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
