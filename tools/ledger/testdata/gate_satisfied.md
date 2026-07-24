# Fixture: the LEGITIMATE way out of an outstanding verification state.
#
# Guards the new "evidence absent" rule against OVER-rejection: a step that
# genuinely ran its deferred mandatory Verify may become `passed`, and the
# ledger records that as the gate flipping `pending-mandatory` -> `satisfied`
# with its evidence note.
#
# This is the exact shape S2 will take once its runtime Verify runs on an
# unrestricted host: same `verify-pending -> passed` transition as
# `erased_evidence.md`, but with the positive evidence present instead of
# deleted. A conforming validator MUST accept this — and only this — direction.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S2: Docker-compose runtime boot + PostgreSQL 18.x assertion + Spotlight UI — EXECUTED on an unrestricted host, see gate note.
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | satisfied | S2 exact Verify EXECUTED on an unrestricted host: task dev boot exit 0, docker compose ps all healthy, select version() == PostgreSQL 18.x, task db:reset up+down, Spotlight UI :8969 reachable
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | runtime Verify executed; gate satisfied |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | runtime Verify executed on unrestricted host, all checks green | unrestricted-host run
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
