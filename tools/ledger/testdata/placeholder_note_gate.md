# Fixture: a `satisfied` GATE row whose note is a VISIBLE placeholder (issue #19, cycle-2)
#
# The visible twin of `zwsp_note_gate.md`: the note is a single hyphen — the
# "nothing to say here" filler every table collects. It is non-empty and
# non-whitespace, so a strip()-based rule accepts it, yet it records exactly as
# much about an executed check as the empty note does: nothing.
#
# A conforming validator MUST reject a `satisfied` note that contains no
# letter or digit in ANY script.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | satisfied | -
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) hyphen-placeholder `satisfied` note standing in for evidence |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind a punctuation-only gate note | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
