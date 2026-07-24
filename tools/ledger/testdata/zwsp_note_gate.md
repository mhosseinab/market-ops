# Fixture: a `satisfied` GATE row whose note is an INVISIBLE placeholder (issue #19, cycle-2)
#
# `empty_note_gate.md` pinned the contentless `satisfied` token. Requiring only a
# non-whitespace note does NOT close that class: `str.strip()` removes Unicode
# whitespace (Zs/Cc) but not FORMAT characters (Cf). The note below is a single
# ZERO-WIDTH SPACE (U+200B) — it renders visually identical to the empty note in
# `empty_note_gate.md`, is indistinguishable from it in review, and is exactly
# the shape a step would take to claim a verification that never ran.
#
# The evidence rule must key on whether the note carries MEANING, not on whether
# it carries bytes. A conforming validator MUST reject this as it rejects
# `empty_note_gate.md`.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | satisfied | ​
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) zero-width-space `satisfied` note standing in for evidence |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind an invisible-placeholder gate note | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
