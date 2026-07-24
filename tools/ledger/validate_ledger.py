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
  * **absent** evidence: a step transitioning to ``passed`` OUT of an
    outstanding-verification state (``verify-pending``/``blocked``) with no
    ``satisfied`` GATE row. Evidence that is missing is not evidence that is
    good — without this, the rule above was bypassable by simply DELETING the
    contradicting gate row (see ``testdata/erased_evidence.md``).

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
    errors: list[str] = []
    lines = text.splitlines()
    inside = False
    seen_block = False
    for line in lines:
        if BEGIN_MARK in line:
            inside = True
            seen_block = True
            continue
        if END_MARK in line:
            inside = False
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
    errors: list[str] = []
    inside = False
    seen_block = False
    for line in text.splitlines():
        if TXN_BEGIN_MARK in line:
            inside = True
            seen_block = True
            continue
        if TXN_END_MARK in line:
            inside = False
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
    """Require POSITIVE evidence for a step that leaves an outstanding state.

    The first remediation of issue #19 rejected `passed` only when a
    contradicting ``pending-mandatory`` GATE row was PRESENT. That makes the
    enforcement bypassable by DELETION: erase the gate row and the deferred
    bullet, flip the status, and the record validates clean — mandatory
    verification evidence is then ABSENT rather than negative, which the issue
    names as an equally-rejectable condition.

    The transition log is the deletion-resistant anchor. Parity already requires
    a producing transition for every non-initial table state, so a step cannot
    reach `passed` out of `verify-pending`/`blocked` without leaving a TXN row
    behind. That row is the trigger: it demands a `satisfied` gate carrying the
    evidence that the previously-outstanding mandatory verification actually ran.

    Keyed on the transition rather than on step identity, so it applies
    consistently to ALL steps, present and future. A step that went straight
    `pending -> passed` never declared an outstanding mandatory gate and is
    governed by the existing Rule 1 / Rule 2 pair instead.
    """
    violations: list[str] = []
    for step, prev, new in transitions:
        if new != "passed" or prev not in OUTSTANDING_VERIFICATION_STATES:
            continue
        entries = gates.get(step, [])
        if not any(state in SATISFYING_GATES for state, _ in entries):
            present = ", ".join(sorted({state for state, _ in entries})) or "none"
            violations.append(
                f"{step}: transition '{prev} -> passed' claims a previously "
                f"outstanding MANDATORY verification is now complete, but the "
                f"verification-gate registry holds no `satisfied` GATE row for "
                f"{step} (gate rows present: {present}). Mandatory verification "
                f"evidence is ABSENT, which never satisfies a dependency gate — "
                f"record the executed checks as `GATE {step} | satisfied | "
                f"<evidence>` or keep {step} at '{prev}'."
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

    # Rule 4 — leaving an outstanding-verification state for `passed` requires
    # POSITIVE evidence, so erasing a gate row cannot launder an unverified step.
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
    text = path.read_text(encoding="utf-8")

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
