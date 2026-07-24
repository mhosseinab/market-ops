# Fixture: casing bypass of the `passed` gate rule (issue #19, second remediation)
#
# Byte-for-byte the `inconsistent.md` record — S2 `passed` while carrying a
# `pending-mandatory` MANDATORY gate — with ONE difference: the status token is
# spelled `Passed` instead of `passed`, in both the status table and the
# transition log.
#
# The parity check canonicalises status tokens (spelling is data, not identity),
# so parity still holds and the record still reads as `passed` to every human
# and to the dependency-gate rules. If the gate rule compares the RAW string it
# silently stops firing, and a one-character edit unlocks dependents from an
# unverified step.
#
# A conforming validator MUST reject this exactly as it rejects inconsistent.md.

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
| S2 | Dev stack | Passed | 2 | dk-p0/S2 | ee97605 | (bypass) casing variant of the same unverified claim |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> Passed | (bug) recorded passed while runtime Verify still pending | ee97605
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
