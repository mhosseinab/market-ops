# Fixture: reconciled ledger (the honest post-fix state of issue #19)
#
# S2 is moved out of `passed` into `verify-pending` because its exact runtime
# Verify remains a pending MANDATORY gate. S6's mandatory Verify is satisfied,
# so it may stay `passed`. A conforming validator MUST accept this.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S2: Docker-compose runtime boot + PostgreSQL 18.x assertion + Spotlight UI — Docker-image-gated; run on an unrestricted host.
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | pending-mandatory | S2 exact Verify: task dev / compose ps / select version()==PG18.x / Spotlight :8969 — never executed, Docker+egress gated
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | verify-pending | 2 | dk-p0/S2 | ee97605 | runtime boot pending on unrestricted host |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | merged; runtime Verify gated to unrestricted host | ee97605
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
