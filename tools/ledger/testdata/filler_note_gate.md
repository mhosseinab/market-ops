# Fixture: a `satisfied` note built from INVISIBLE-BUT-ALPHANUMERIC fillers (issue #19, cycle-3)
#
# `zwsp_note_gate.md` pins U+200B, a FORMAT character (Cf): `str.isalnum()` is
# False for it, so the content test already rejected it. This fixture pins the
# other spelling of the same review-undetectability class: U+115F HANGUL CHOSEONG
# FILLER, U+1160 HANGUL JUNGSEONG FILLER, U+3164 HANGUL FILLER and U+FFA0
# HALFWIDTH HANGUL FILLER are Unicode category Lo — LETTERS — so `isalnum()` is
# True for each, yet all four render blank in every common font.
#
# A reviewer cannot tell the note below from the empty note in
# `empty_note_gate.md` or the zero-width note in `zwsp_note_gate.md`. The
# validator must not accept what a reviewer cannot see, so these four fillers are
# stripped before the content test. This is a fixed codepoint set, not a
# language or phrasing branch (CLAUDE.md §11): notes in any script are unaffected.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | satisfied | ᅟᅠㅤﾠ
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | (bypass) invisible Hangul-filler `satisfied` note standing in for evidence |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | (bypass) claims verification behind an invisible-filler gate note | none
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
