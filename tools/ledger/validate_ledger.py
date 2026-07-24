#!/usr/bin/env python3
"""Orchestration-ledger verification-gate validator (issue #19).

Enforces one ledger-integrity rule across the whole progress ledger
(`docs/implementation/dk-p0-progress.md`):

    A step may be recorded ``passed`` — and thereby satisfy a dependency gate —
    ONLY after every MANDATORY verification has a successful evidence record.
    A step whose exact Verify block is recorded as a still-pending MANDATORY
    verification gate must NOT be ``passed`` (it stays ``verify-pending`` /
    ``blocked``). Deferred/skipped mandatory verification never satisfies a
    dependency gate.

This mirrors the plan's exact verification-and-unlock rules: skipped or
environment-blocked MANDATORY verification is explicitly non-satisfying. The
rule is applied consistently to ALL steps, not just the one that motivated it.

The validator reads two structures from the ledger and fails closed:

1. The **Status table** — the ``| Step | ... | Status | ...`` rows. Only the
   first three columns (before the free-text Note) are read, so notes may
   contain ``|`` without breaking parsing.

2. The machine-checked **verification-gate registry**, a comment block::

       <!-- LEDGER-VERIFICATION-GATES:BEGIN
       GATE S2 | pending-mandatory | <evidence / basis note>
       ...
       LEDGER-VERIFICATION-GATES:END -->

   Each ``GATE`` row classifies one deferred/verification item:

     - ``pending-mandatory``     part of the step's exact Verify block and NOT
                                 yet satisfied -> BLOCKS ``passed``.
     - ``satisfied``             the gate's evidence is recorded successful.
     - ``deferred-progress-gate`` the step doc explicitly labels it
                                 "Deferred (progress-file gate)", separate from
                                 the step's mandatory Verify (which passed).
     - ``release-gate``          belongs to a later human-gated step (S34/S35),
                                 not part of an already-``passed`` step's Verify.

Three failure classes, all fail closed:
  * a ``passed`` step carrying a ``pending-mandatory`` gate (the issue #19 bug),
    matched on the CANONICAL status token so a respelling (``Passed``) cannot
    silently disable the rule;
  * ledger/registry drift: an unknown gate state, a gate for a step absent from
    the status table, or a ``- S<N>:`` bullet in the human "Deferred verification
    gate" section with no corresponding ``GATE`` row (an unclassified deferral
    could otherwise hide an unverified ``passed``);
  * **absent** evidence: a step whose transition HISTORY ever entered an
    outstanding-verification state (``verify-pending``/``blocked``) and whose
    replay-derived final state is ``passed``, with no ``satisfied`` GATE row
    carrying a non-empty evidence note. Evidence that is missing is not evidence
    that is good — without this, the rule above was bypassable by simply
    DELETING the contradicting gate row (``testdata/erased_evidence.md``), by
    routing around it through a longer legal chain
    (``testdata/hop_laundered.md``), or by writing a ``satisfied`` token with no
    record behind it — empty (``testdata/empty_note_gate.md``), invisible via a
    format character (``testdata/zwsp_note_gate.md``) or via an
    invisible-but-alphanumeric Hangul filler (``testdata/filler_note_gate.md``),
    or punctuation-only (``testdata/placeholder_note_gate.md``).

    This rule does NOT cover, among other residuals, a step logged straight
    ``pending -> passed``, a ``passed -> reopened -> passed`` re-pass, or
    per-GATE-item erasure when a step has several mandatory items. The note test
    is a floor on FORM, not on truth. See ``validate_unlock_evidence`` for the
    full residual list and why each needs a policy/schema decision rather than a
    validator tweak.

3. The machine-checked **transition log**, a comment block (issue #20)::

       <!-- LEDGER-TRANSITIONS:BEGIN
       TXN S1 | pending -> passed | <reason / evidence> | <commit/check ref>
       ...
       LEDGER-TRANSITIONS:END -->

   Each ``TXN`` row records one ordered state change: previous state, new state,
   a reason/evidence note, and a relevant commit/check reference. The **parity
   check** replays these rows in file order, deriving each step's current state
   from the initial ``pending`` state, and asserts the derived state EXACTLY
   equals the status table. This closes the issue-#20 gap where a status-table
   cell could diverge from the chronological log with no enforcement — a silent
   table edit masquerading as a valid transition.

   Parity fails closed on:
     * an **unlogged table change** — a non-initial table state with no producing
       transition;
     * an **illegal transition** — an edge the state machine forbids (e.g.
       ``passed -> in_progress`` without a ``reopened``/``regressed`` marker), a
       transition whose declared previous state does not match the replayed
       state (a broken chain), or an unknown state token;
     * **log/table divergence** — the replay-derived current state differs from
       the status-table state.

   The state machine handles ``blocked``, ``in-progress``/``in_progress``,
   ``passed``, ``verify-pending``, ``reopened`` and ``regressed`` (``pending`` is
   the implicit initial state; a ``pending`` table step needs no transition).
"""
from __future__ import annotations

import argparse
import pathlib
import re
import sys

DEFAULT_LEDGER = "docs/implementation/dk-p0-progress.md"

# Gate states that are legal to declare in the registry.
KNOWN_STATES = {
    "pending-mandatory",
    "satisfied",
    "deferred-progress-gate",
    "release-gate",
}
# Only these states forbid a `passed` status.
BLOCKS_PASSED = {"pending-mandatory"}
# Gate states that count as POSITIVE evidence that a step's mandatory
# verification actually completed successfully. `deferred-progress-gate` and
# `release-gate` deliberately do NOT count: they classify an item as being
# outside the step's mandatory Verify, which is a reason the step never had an
# outstanding mandatory gate — not proof that a gated verification ran.
SATISFYING_GATES = {"satisfied"}
# States that mean "this step's mandatory verification is still outstanding".
# Leaving one of them for `passed` is exactly the moment the plan's
# verification-and-unlock rule bites, so it requires positive evidence.
OUTSTANDING_VERIFICATION_STATES = {"verify_pending", "blocked"}
# Codepoints that are Unicode LETTERS (category Lo) — so `str.isalnum()` is True
# for them — but that render blank in every common font. A note built from these
# is indistinguishable in review from an empty note, so they are stripped before
# the evidence-content test. A fixed codepoint set, never a language branch.
INVISIBLE_LETTER_FILLERS = frozenset(
    {
        "ᅟ",  # HANGUL CHOSEONG FILLER
        "ᅠ",  # HANGUL JUNGSEONG FILLER
        "ㅤ",  # HANGUL FILLER
        "ﾠ",  # HALFWIDTH HANGUL FILLER
    }
)

STEP_RE = re.compile(r"^S\d+$")
GATE_RE = re.compile(r"^GATE\s+(S\d+)\s*\|\s*([A-Za-z-]+)\s*\|")
BEGIN_MARK = "LEDGER-VERIFICATION-GATES:BEGIN"
END_MARK = "LEDGER-VERIFICATION-GATES:END"
DEFERRED_HEADING = "Deferred verification gate"
DEFERRED_BULLET_RE = re.compile(r"^-\s+(S\d+)\s*:")

# --- Transition-log parity (issue #20) -------------------------------------
TXN_BEGIN_MARK = "LEDGER-TRANSITIONS:BEGIN"
TXN_END_MARK = "LEDGER-TRANSITIONS:END"
# TXN S<N> | <prev> -> <new> | <reason/evidence> | <commit/check ref>
TXN_RE = re.compile(
    r"^TXN\s+(S\d+)\s*\|\s*([A-Za-z_-]+)\s*->\s*([A-Za-z_-]+)\s*\|"
)
# The implicit initial state every step starts from before any transition.
INITIAL_STATE = "pending"
# Canonical status vocabulary. `in-progress`/`in_progress` and
# `verify-pending`/`verify_pending` are the same state (spelling is data).
KNOWN_STATES_MACHINE = {
    "pending",
    "in_progress",
    "passed",
    "verify_pending",
    "blocked",
    "reopened",
    "regressed",
}
# Legal edges of the ledger state machine. Anything not listed is forbidden.
# A `passed` step may only revert through an explicit `reopened`/`regressed`
# marker — never silently back to `in_progress`.
LEGAL_TRANSITIONS: dict[str, set[str]] = {
    "pending": {"in_progress", "passed", "verify_pending", "blocked"},
    "in_progress": {"passed", "verify_pending", "blocked", "reopened", "regressed"},
    "verify_pending": {"passed", "blocked"},
    "blocked": {"in_progress", "passed"},
    "passed": {"reopened", "regressed"},
    "reopened": {"in_progress", "passed"},
    "regressed": {"in_progress", "passed"},
}


def canon_state(raw: str) -> str:
    """Normalise a status token: spelling and separators are data, not identity."""
    return raw.strip().lower().replace("-", "_")


def has_evidence_content(note: str) -> bool:
    """True when a gate note carries at least one letter, digit or numeric char.

    A `satisfied` GATE row IS the evidence record, so the note must say
    something. `str.strip()` is not enough to test that: it removes Unicode
    whitespace (categories Zs/Cc) but NOT format characters (Cf), so a single
    ZERO-WIDTH SPACE (U+200B) passes it while rendering identically to an empty
    note; punctuation placeholders (`-`, `.`, `?`) pass it too.

    The test is `str.isalnum()`, which is Unicode-aware, over any single
    character — deliberately NOT an ASCII `[0-9A-Za-z]` test. Locale is data
    (CLAUDE.md §11): a real evidence note written in Persian, with Persian-Indic
    digits and ZWNJ inside words, is a genuine record and must be accepted. An
    ASCII test would reject it and push evidence notes into English, which is a
    localization-boundary violation, not a safety gain. MEASURED: a Persian-Indic
    digit (`۵`) is accepted, exactly as that choice predicts; `testdata/
    persian_note_gate.md` pins the accept and exits 1 under an ASCII variant.

    `isalnum()` alone is not a VISIBILITY test, though: the four Hangul fillers
    in `INVISIBLE_LETTER_FILLERS` are category Lo (letters), so `isalnum()` is
    True for each while all four render blank in every common font. They are
    removed before the test, so a note built only from them is rejected like an
    empty one — MEASURED on `testdata/filler_note_gate.md`. This is a fixed
    codepoint set, not a language or phrasing branch: no note in any script is
    affected by it.

    What is closed, precisely: a `satisfied` note that is empty,
    whitespace-only, made of U+200B/U+200C, made of these four fillers, or
    punctuation/symbol-only. What is NOT closed: any other invisible or
    confusable codepoint outside that set, and the note's TRUTH — this is a floor
    on FORM only. See `validate_unlock_evidence` for the full residual list.
    """
    visible = "".join(ch for ch in note if ch not in INVISIBLE_LETTER_FILLERS)
    return any(ch.isalnum() for ch in visible)


def check_block_delimiters(text: str, begin_mark: str, end_mark: str) -> list[str]:
    """Fail closed unless each delimiter appears EXACTLY once, BEGIN before END.

    The delimiters are structure, not prose. `parse_gate_registry` and
    `parse_transition_log` set `inside = True` on the BEGIN line and clear it only
    on an END line, so any shape that leaves the block open runs it to EOF and
    every `GATE`/`TXN` row in ordinary document body is honoured as a registry
    row. Four shapes reach that same payload, all MEASURED as accepted (exit 0)
    before this check counted them:

    * BEGIN and END on ONE line (`testdata/registry_collapsed_block.md`,
      `testdata/transitions_collapsed_block.md`);
    * a MISSING END (`testdata/registry_unterminated_block.md`,
      `testdata/transitions_unterminated_block.md`) — reached by deleting one
      marker rather than joining two;
    * END before BEGIN with both present exactly once
      (`testdata/registry_out_of_order_block.md`) — marker COUNTS alone do not
      make a block well formed;
    * a duplicated marker (`testdata/registry_truncated_block.md`) — any line
      merely CONTAINING the END token closes the block, so prose could silently
      truncate the registry and hide the rows after it (a contradicting
      `pending-mandatory` row, for instance).

    So: `begin_mark` on exactly one line, `end_mark` on exactly one line, the
    BEGIN line strictly before the END line, and never the same line. This is a
    well-formedness check on the two delimiter tokens ONLY. It does not make the
    block immutable, and it says nothing about the rows inside it.
    """
    errors: list[str] = []
    lines = text.splitlines()
    begin_lines = [i for i, line in enumerate(lines) if begin_mark in line]
    end_lines = [i for i, line in enumerate(lines) if end_mark in line]
    both = [lines[i] for i in begin_lines if i in set(end_lines)]
    if both:
        errors.append(
            f"{begin_mark} and {end_mark} appear on the SAME line "
            f"({both[0].strip()!r}) — a collapsed delimiter is not an open block "
            f"(fail closed)."
        )
    if len(begin_lines) != 1:
        errors.append(
            f"{begin_mark} appears on {len(begin_lines)} lines — it must appear "
            f"exactly once."
        )
    if len(end_lines) != 1:
        errors.append(
            f"{end_mark} appears on {len(end_lines)} lines — it must appear "
            f"exactly once; a missing terminator leaves the block open to EOF and "
            f"a duplicate one silently truncates it."
        )
    if len(begin_lines) == 1 and len(end_lines) == 1 and begin_lines[0] >= end_lines[0]:
        errors.append(
            f"{end_mark} (line {end_lines[0] + 1}) does not follow {begin_mark} "
            f"(line {begin_lines[0] + 1}) — a terminator that precedes its opener "
            f"closes nothing and leaves the block open to EOF (fail closed)."
        )
    return errors


def parse_status_table(text: str) -> dict[str, str]:
    """Map step id -> status from the markdown status table.

    Robust to ``|`` inside the free-text Note column: status is the third
    content column and is read positionally, before any note.
    """
    statuses: dict[str, str] = {}
    for line in text.splitlines():
        stripped = line.strip()
        if not stripped.startswith("|"):
            continue
        cells = [c.strip() for c in stripped.split("|")]
        # cells[0] is empty (leading pipe). Content columns start at index 1.
        if len(cells) < 4:
            continue
        step, status = cells[1], cells[3]
        if STEP_RE.match(step):
            statuses[step] = status
    return statuses


def parse_gate_registry(text: str) -> tuple[dict[str, list[tuple[str, str]]], list[str]]:
    """Parse the machine-checked gate block.

    Returns (gates, errors) where gates maps step -> list of (state, note).
    """
    gates: dict[str, list[tuple[str, str]]] = {}
    errors: list[str] = check_block_delimiters(text, BEGIN_MARK, END_MARK)
    lines = text.splitlines()
    inside = False
    seen_block = False
    for line in lines:
        # END is tested FIRST: a line carrying both markers closes (never opens)
        # the block, so a collapsed delimiter cannot leave it open forever.
        if END_MARK in line:
            inside = False
            continue
        if BEGIN_MARK in line:
            inside = True
            seen_block = True
            continue
        if not inside:
            continue
        stripped = line.strip()
        if not stripped or not stripped.startswith("GATE"):
            continue
        m = GATE_RE.match(stripped)
        if not m:
            errors.append(f"malformed GATE row (expected 'GATE S<N> | <state> | ...'): {stripped!r}")
            continue
        step, state = m.group(1), m.group(2)
        parts = stripped.split("|", 2)
        note = parts[2].strip() if len(parts) > 2 else ""
        if state not in KNOWN_STATES:
            errors.append(
                f"{step}: unknown gate state {state!r} "
                f"(known: {', '.join(sorted(KNOWN_STATES))})"
            )
        gates.setdefault(step, []).append((state, note))
    if not seen_block:
        errors.append(
            "no LEDGER-VERIFICATION-GATES block found — the machine-checked "
            "verification-gate registry is missing (fail closed)."
        )
    return gates, errors


def parse_deferred_bullets(text: str) -> set[str]:
    """Collect step ids from the human 'Deferred verification gate' section."""
    steps: set[str] = set()
    lines = text.splitlines()
    in_section = False
    for line in lines:
        stripped = line.strip()
        if stripped.startswith("#"):
            in_section = DEFERRED_HEADING in stripped
            continue
        if not in_section:
            continue
        m = DEFERRED_BULLET_RE.match(stripped)
        if m:
            steps.add(m.group(1))
    return steps


def parse_transition_log(
    text: str,
) -> tuple[list[tuple[str, str, str]], list[str]]:
    """Parse the machine-checked transition-log block (issue #20).

    Returns (transitions, errors) where transitions is an ordered list of
    (step, prev_state, new_state) with states canonicalised. Malformed rows and
    an absent block are reported as errors (fail closed).
    """
    transitions: list[tuple[str, str, str]] = []
    errors: list[str] = check_block_delimiters(text, TXN_BEGIN_MARK, TXN_END_MARK)
    inside = False
    seen_block = False
    for line in text.splitlines():
        # END first — see `check_block_delimiters`.
        if TXN_END_MARK in line:
            inside = False
            continue
        if TXN_BEGIN_MARK in line:
            inside = True
            seen_block = True
            continue
        if not inside:
            continue
        stripped = line.strip()
        if not stripped or not stripped.startswith("TXN"):
            continue
        m = TXN_RE.match(stripped)
        if not m:
            errors.append(
                f"malformed TXN row (expected 'TXN S<N> | <prev> -> <new> | "
                f"<reason> | <ref>'): {stripped!r}"
            )
            continue
        step, prev, new = m.group(1), canon_state(m.group(2)), canon_state(m.group(3))
        transitions.append((step, prev, new))
    if not seen_block:
        errors.append(
            "no LEDGER-TRANSITIONS block found — the machine-checked "
            "transition log is missing, so status-table parity cannot be "
            "verified (fail closed)."
        )
    return transitions, errors


def validate_parity(
    statuses: dict[str, str], transitions: list[tuple[str, str, str]]
) -> list[str]:
    """Replay the ordered transition log and assert parity with the status table.

    Fails closed on an unlogged table change, an illegal transition, a broken
    chain, an unknown state, or log/table divergence.
    """
    violations: list[str] = []
    derived: dict[str, str] = {}
    logged_steps: set[str] = set()

    for step, prev, new in transitions:
        logged_steps.add(step)
        if step not in statuses:
            violations.append(
                f"{step}: transition '{prev} -> {new}' logged but the step is "
                f"absent from the status table."
            )
        if prev not in KNOWN_STATES_MACHINE:
            violations.append(f"{step}: unknown previous state {prev!r} in transition log.")
        if new not in KNOWN_STATES_MACHINE:
            violations.append(f"{step}: unknown new state {new!r} in transition log.")
        current = derived.get(step, INITIAL_STATE)
        if prev != current:
            violations.append(
                f"{step}: broken transition chain — '{prev} -> {new}' starts "
                f"from '{prev}' but the replayed current state is '{current}'."
            )
        elif new not in LEGAL_TRANSITIONS.get(prev, set()):
            violations.append(
                f"{step}: illegal transition '{prev} -> {new}' — the ledger "
                f"state machine forbids this edge."
            )
        derived[step] = new

    for step, raw_status in sorted(statuses.items()):
        table_state = canon_state(raw_status)
        if table_state == INITIAL_STATE:
            # `pending` is the initial state — it needs no producing transition.
            continue
        if step not in logged_steps:
            violations.append(
                f"{step}: status table shows '{raw_status}' but no transition-log "
                f"entry produces it (unlogged table change — fail closed)."
            )
            continue
        final = derived.get(step, INITIAL_STATE)
        if final != table_state:
            violations.append(
                f"{step}: log/table divergence — the transition log derives "
                f"'{final}' but the status table says '{raw_status}'."
            )

    return violations


def validate_unlock_evidence(
    gates: dict[str, list[tuple[str, str]]],
    transitions: list[tuple[str, str, str]],
) -> list[str]:
    """Require POSITIVE evidence from any step that ever declared one outstanding.

    The first remediation of issue #19 rejected `passed` only when a
    contradicting ``pending-mandatory`` GATE row was PRESENT. That makes the
    enforcement bypassable by DELETION: erase the gate row and the deferred
    bullet, flip the status, and the record validates clean — mandatory
    verification evidence is then ABSENT rather than negative, which the issue
    names as an equally-rejectable condition.

    Precisely what this rule guarantees
    -----------------------------------
    The trigger is the step's transition HISTORY, not the edge that happens to
    end at `passed`. The log is replayed per step, recording (a) whether the step
    was EVER in an outstanding-verification state (``verify-pending`` /
    ``blocked``) and (b) its derived final state. Every step that was ever
    outstanding and derives `passed` must carry a `satisfied` GATE row whose
    evidence note contains at least one letter or digit (`has_evidence_content`).

    Keying on the immediate predecessor was not enough: `verify-pending ->
    blocked -> in-progress -> passed` is a fully legal chain whose last edge
    leaves `in_progress`, so a predecessor-keyed rule never fired while Rule 1
    (needs a `pending-mandatory` row) and Rule 2 (needs a deferred bullet) both
    stay silent once those two lines are deleted — the same unverified `passed`,
    two extra rows. History-keying closes every re-ordering of that chain.

    The note requirement is a floor on the FORM of the record, and only that.
    MEASURED, not intended: it rejects a `satisfied` note that is empty,
    whitespace-only, built from the invisible format characters U+200B / U+200C,
    built from the four invisible Hangul fillers U+115F / U+1160 / U+3164 /
    U+FFA0 (`INVISIBLE_LETTER_FILLERS`), or punctuation/symbol-only (`-`, `.`,
    `?`); it ACCEPTS any note carrying one letter, digit or numeric character in
    any script — including `x`, `n/a`, `see above`, `TBD, not run yet`, `۵`,
    `Ⅷ` and `①`.

    Stated exactly: for the invisible-note class it closes the SIX codepoints
    named above, which are the ones with a pinned fixture. It is NOT a general
    invisibility test — any other blank-rendering or confusable codepoint outside
    that set still passes, and no fixture claims otherwise. It cannot tell a thin
    note from a false one; that residual is named below.

    What this rule does NOT guarantee (known residuals, deliberately in the open)
    ---------------------------------------------------------------------------
    * The note is never checked for TRUTH or substance. ``GATE S<N> | satisfied |
      n/a`` and ``... | TBD, not run yet`` both pass — a note stating in words
      that the check did not run still satisfies the rule. No validator can prove
      a note describes an executed run; closing this needs a registry-schema
      change (a REQUIRED commit/check reference on GATE rows, as TXN rows already
      carry, checkable against CI). A denylist of placeholder phrasings is
      explicitly rejected as a fix: it would put a natural-language branch in
      core logic, which the localization boundary forbids, and would be evaded by
      writing the same placeholder in another language.
    * A step logged straight ``pending -> passed`` is NOT covered here. Rule 1
      fires only when a `pending-mandatory` row is present, and Rule 2 only when
      the deferred bullet is written in the exact ``- S<N>:`` shape the parser
      matches (``* S<N>:``, ``1. S<N>:`` and bullets under a sub-heading are
      invisible to it) — so Rule 2 is a weak compensating control, not a
      backstop. Erase or merely reshape both and such a step is UNGUARDED; about
      thirty steps in the real ledger carry no GATE row at all. Closing it needs
      a registry-completeness policy (every step must declare its
      mandatory-verification classification) — a schema/policy decision, not a
      validator tweak.
    * Every rule only sees rows its parser matches. A status row written
      ``| **S2** |`` or ``| s2 |`` does not match the step-cell pattern and is
      invisible to all four rules. Widening those patterns is a separate,
      fixture-backed change.
    * DUPLICATE status rows for one step are LAST-WINS
      (``parse_status_table``: ``statuses[step] = status``, pre-existing and
      untouched). MEASURED on ``testdata/erased_evidence.md`` (exit 1): append a
      second ``| S2 | Dev stack | verify-pending | ... |`` row inside a trailing
      HTML comment and drop the ``verify-pending -> passed`` TXN row, and the
      machine reads ``verify-pending`` — parity holds, Rule 4 never fires, the
      file exits 0 — while the rendered table a human reads still shows S2 as
      ``passed``. The rendered table and the parsed table can disagree. Closing
      this means rejecting duplicate step rows outright — a status-table schema
      decision, not a tweak inside these rules.
    * ``has_evidence_content`` accepts Nl/No numeric characters as content, not
      only Ll/Lu letters and Nd digits: ``Ⅷ``, ``①`` and ``²`` are each accepted
      (MEASURED). That is the intended Unicode-aware floor, but it means the
      accepted set is wider than "letter or digit".
    * ``passed -> reopened -> passed`` (and the `regressed` twin) is likewise not
      covered: `reopened`/`regressed` are not outstanding-verification states, and
      the accepted ``testdata/parity_reopened.md`` fixture pins that as legal
      today. Treating a re-pass as evidence-demanding is the same policy decision.
    * A single `satisfied` row satisfies the whole step, so a step with several
      mandatory gate items can still lose one to erasure. Per-item enforcement
      needs stable item IDs on GATE rows (registry-schema change).
    * The rule reads the transition log, which is an ordinary file section. Its
      delimiters are now well-formedness-checked (`check_block_delimiters`), so a
      collapsed, duplicated, missing or out-of-order marker fails closed rather
      than silently widening or truncating the block, but the section is still
      editable: it is
      deletion-resistant only insofar as parity independently requires a
      producing transition for every non-initial table state; it is not an
      immutable history.
    """
    violations: list[str] = []
    ever_outstanding: set[str] = set()
    derived: dict[str, str] = {}
    for step, prev, new in transitions:
        if prev in OUTSTANDING_VERIFICATION_STATES or new in OUTSTANDING_VERIFICATION_STATES:
            ever_outstanding.add(step)
        derived[step] = new

    for step in sorted(ever_outstanding):
        if derived.get(step, INITIAL_STATE) != "passed":
            continue
        entries = gates.get(step, [])
        satisfying = [(state, note) for state, note in entries if state in SATISFYING_GATES]
        if any(has_evidence_content(note) for _, note in satisfying):
            continue
        if satisfying:
            reason = (
                "the only `satisfied` GATE row(s) carry an evidence note with no "
                "letter or digit in any script (empty, whitespace-only, "
                "invisible-format-character, or punctuation-only) — a state token "
                "is not a record of an executed check"
            )
        else:
            present = ", ".join(sorted({state for state, _ in entries})) or "none"
            reason = (
                f"the verification-gate registry holds no `satisfied` GATE row "
                f"for {step} (gate rows present: {present})"
            )
        violations.append(
            f"{step}: the transition log records {step} in an outstanding "
            f"MANDATORY verification state (verify-pending/blocked) and then "
            f"derives '{derived[step]}', but {reason}. Mandatory verification "
            f"evidence is ABSENT, which never satisfies a dependency gate — "
            f"record the executed checks as `GATE {step} | satisfied | "
            f"<evidence>` or keep {step} out of `passed`."
        )
    return violations


def validate(text: str) -> list[str]:
    """Return a list of violation strings (empty == valid)."""
    violations: list[str] = []
    statuses = parse_status_table(text)
    gates, gate_errors = parse_gate_registry(text)
    violations.extend(gate_errors)
    deferred_steps = parse_deferred_bullets(text)
    transitions, txn_errors = parse_transition_log(text)
    violations.extend(txn_errors)

    if not statuses:
        violations.append("status table not found or empty (fail closed).")

    # Rule 1 — the never-cut ledger-integrity rule, applied to EVERY step.
    for step, entries in gates.items():
        if step not in statuses:
            violations.append(
                f"{step}: gate declared but step is absent from the status table."
            )
            continue
        # Spelling is data, not identity: `Passed`/`passed` are one state, so the
        # rule must key on the canonical token the parity replay already uses.
        # Comparing the raw cell let a one-character edit silently disable this.
        status = canon_state(statuses[step])
        for state, note in entries:
            if state in BLOCKS_PASSED and status == "passed":
                violations.append(
                    f"{step}: status is `passed` but a MANDATORY verification gate "
                    f"is `{state}` (unrun/unsatisfied). A step may become `passed` "
                    f"and unlock dependents only after every mandatory verification "
                    f"succeeds. Evidence: {note}"
                )

    # Rule 2 — every human-listed deferred step must be classified in the
    # registry, so an unclassified deferral cannot silently hide a `passed`.
    for step in sorted(deferred_steps):
        if step not in gates:
            violations.append(
                f"{step}: listed in the 'Deferred verification gate' section but has "
                f"no GATE row in the machine-checked registry (classify it)."
            )

    # Rule 3 — transition-log ⇄ status-table parity (issue #20). The replay
    # derives per-step state from the ordered log and must match the table.
    violations.extend(validate_parity(statuses, transitions))

    # Rule 4 — a step whose HISTORY entered an outstanding-verification state and
    # that derives `passed` requires POSITIVE evidence (a `satisfied` gate row
    # with a real note), so neither erasing the gate row, nor hopping to `passed`
    # via extra legal states, nor an empty-note `satisfied` token launders it.
    # Steps that never declared an outstanding state are NOT covered — see
    # `validate_unlock_evidence` for that residual.
    violations.extend(validate_unlock_evidence(gates, transitions))

    return violations


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--file",
        default=DEFAULT_LEDGER,
        help="path to the progress ledger (default: %(default)s)",
    )
    args = parser.parse_args(argv)

    path = pathlib.Path(args.file)
    if not path.exists():
        print(f"ledger:validate: file not found: {path}", file=sys.stderr)
        return 2
    try:
        text = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        # An unreadable/undecodable ledger is an input error, not a clean run and
        # not a violation — exit 2 with the failing seam named, never a traceback.
        print(f"ledger:validate: cannot read {path}: {exc}", file=sys.stderr)
        return 2

    violations = validate(text)
    if violations:
        print(f"ledger:validate: {len(violations)} violation(s) in {path}:", file=sys.stderr)
        for v in violations:
            print(f"  - {v}", file=sys.stderr)
        return 1
    print(f"ledger:validate: OK — {path} verification-gate integrity holds.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
