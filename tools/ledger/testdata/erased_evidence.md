# Fixture: ABSENT verification evidence (issue #19, second remediation)
#
# The issue's acceptance criterion reads: "A ledger validator rejects `passed`
# when mandatory verification evidence is absent OR unsuccessful."
#
# `inconsistent.md` covers the UNSUCCESSFUL half (the gate row is present and
# says `pending-mandatory`). This fixture covers the ABSENT half, which is the
# strictly easier bypass: S2 previously declared an outstanding mandatory gate
# and sat at `verify-pending`; here it is flipped to `passed` while its GATE row
# and its "Deferred verification gate" bullet are simply DELETED. No evidence of
# any kind remains that S2's runtime Verify ever ran.
#
# The registry block itself is still present (S6 keeps a row), so this is not a
# "missing block" case — it is targeted erasure of one step's evidence. If the
# validator accepts this, the enforcement is bypassable by deletion and the
# defect class of issue #19 is still open.
#
# A conforming validator MUST reject: the transition INTO `passed` out of
# `verify-pending` is an unlogged-evidence claim.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) gate row + deferred bullet deleted, then flipped to passed |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification without any satisfied gate evidence | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
