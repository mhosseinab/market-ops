# Fixture: hop-chain laundering of ABSENT evidence (issue #19, cycle-1 finding)
#
# Same defect as `erased_evidence.md` — S2 reaches `passed` with no record that
# its mandatory runtime Verify ever ran — reached by a longer, entirely LEGAL
# transition chain instead of the single `verify-pending -> passed` edge:
#
#     pending -> verify-pending -> blocked -> in-progress -> passed
#
# Every edge is permitted by the state machine, parity holds, the GATE row and
# the "Deferred verification gate" bullet are deleted (so the pending-mandatory
# rule and the deferred-classification rule both stay silent). The only state
# adjacent to `passed` is `in-progress`, which is not an outstanding-verification
# state — so a rule keyed on the IMMEDIATE predecessor never fires.
#
# The step's HISTORY, not its last edge, is what declares an outstanding
# mandatory verification. A conforming validator MUST reject: S2 was in
# `verify-pending` (and `blocked`), ends at `passed`, and the registry holds no
# `satisfied` GATE row for it.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) routed around the evidence rule via a legal hop chain |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> blocked | env blocked | none
TXN S2 | blocked -> in-progress | picked back up | none
TXN S2 | in-progress -> passed | (bypass) no verification ever ran | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
