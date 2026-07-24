# Fixture: ANTI-OVER-REJECTION — a legitimate Persian-script evidence note.
#
# The note-content rule must reject notes that carry no meaning, NEVER notes
# that merely carry no LATIN characters. Locale is data (CLAUDE.md §11): an
# operator recording a real, executed Verify in Persian — including ZWNJ
# (U+200C) inside words and Persian-Indic digits — has produced a genuine
# evidence record, and the validator must accept it.
#
# This pins the deliberate choice of a UNICODE-aware letter/digit test
# (`str.isalnum()` over any character) instead of an ASCII `[0-9A-Za-z]` test,
# which would reject this row and push evidence notes into English.
#
# The S2 gate note below contains NO ASCII alphanumeric character — that is what
# makes the pin discriminating rather than decorative. MEASURED: swapping the
# content test for an ASCII `[0-9A-Za-z]` predicate makes this fixture exit 1,
# while it exits 0 under the shipped Unicode-aware test. (An earlier revision
# used the Latin word `healthy` inside the Persian note, which an ASCII predicate
# would also have accepted — the fixture then exited 0 under BOTH predicates and
# pinned nothing.) The S6 row keeps Latin text on purpose: S6 never entered an
# outstanding-verification state, so Rule 4 never reads its note.

## ⚠️ Deferred verification gate (run before S36 sign-off)
- S6: first push to GitHub — all CI jobs green.

<!-- LEDGER-VERIFICATION-GATES:BEGIN
GATE S2 | satisfied | راه‌اندازی کامل روی میزبان بدون محدودیت اجرا شد؛ همهٔ سرویس‌ها سالم، نسخهٔ پایگاه داده ۱۸ تأیید شد
GATE S6 | satisfied | first-GitHub-run CI green on 71aadfc; mandatory ci:local + actionlint passed
LEDGER-VERIFICATION-GATES:END -->

## Status table

| Step | Title | Status | Attempts | Branch | Commit SHA | Note |
|------|-------|--------|----------|--------|-----------|------|
| S1 | Scaffold | passed | 1 | dk-p0/S1 | fd58883 | ok |
| S2 | Dev stack | passed | 2 | dk-p0/S2 | ee97605 | runtime Verify executed on an unrestricted host; evidence recorded in Persian |
| S6 | CI pipeline | passed | 2 | dk-p0/S6 | 138b85e | CI green |

<!-- LEDGER-TRANSITIONS:BEGIN
TXN S1 | pending -> passed | scaffold merged, review green | fd58883
TXN S2 | pending -> verify-pending | runtime Verify Docker/egress gated | ee97605
TXN S2 | verify-pending -> passed | اجرای Verify زمان اجرا روی میزبان بدون محدودیت | unrestricted-host run
TXN S6 | pending -> passed | CI pipeline merged, first-GitHub-run green | 138b85e
LEDGER-TRANSITIONS:END -->

> Status values: pending | in_progress | passed | verify-pending | blocked.
