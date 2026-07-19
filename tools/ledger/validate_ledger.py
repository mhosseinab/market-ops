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

Two failure classes, both fail closed:
  * a ``passed`` step carrying a ``pending-mandatory`` gate (the issue #19 bug);
  * ledger/registry drift: an unknown gate state, a gate for a step absent from
    the status table, or a ``- S<N>:`` bullet in the human "Deferred verification
    gate" section with no corresponding ``GATE`` row (an unclassified deferral
    could otherwise hide an unverified ``passed``).
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

STEP_RE = re.compile(r"^S\d+$")
GATE_RE = re.compile(r"^GATE\s+(S\d+)\s*\|\s*([A-Za-z-]+)\s*\|")
BEGIN_MARK = "LEDGER-VERIFICATION-GATES:BEGIN"
END_MARK = "LEDGER-VERIFICATION-GATES:END"
DEFERRED_HEADING = "Deferred verification gate"
DEFERRED_BULLET_RE = re.compile(r"^-\s+(S\d+)\s*:")


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


def validate(text: str) -> list[str]:
    """Return a list of violation strings (empty == valid)."""
    violations: list[str] = []
    statuses = parse_status_table(text)
    gates, gate_errors = parse_gate_registry(text)
    violations.extend(gate_errors)
    deferred_steps = parse_deferred_bullets(text)

    if not statuses:
        violations.append("status table not found or empty (fail closed).")

    # Rule 1 — the never-cut ledger-integrity rule, applied to EVERY step.
    for step, entries in gates.items():
        if step not in statuses:
            violations.append(
                f"{step}: gate declared but step is absent from the status table."
            )
            continue
        status = statuses[step]
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
