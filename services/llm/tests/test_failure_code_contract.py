"""The §12.4 failure-code contract: declared here, mapped at the web edge (#108).

Two guards, in the direction the data flows.

1. **Producer-side (this plane owns it).** Every ``failure.code`` literal the LLM
   plane can put on a ``failure`` frame is declared in
   :data:`~llm.envelope.models.EMITTABLE_FAILURE_CODES`, and nothing else is. The
   declaration is kept authoritative by SCANNING the emitting modules' source
   rather than by trusting a hand-written copy, and by pinning WHICH modules may
   construct a :class:`~llm.envelope.models.TurnFailure` at all — so a new code,
   or a new emitter, breaks here instead of drifting.

2. **Cross-boundary.** Every declared code must exist in the web edge's CLOSED
   ``FAILURE_CODE_KEY`` map (``apps/web/src/chat/catalogMaps.ts``). An unmapped
   code renders the generic ``chat.failure.unsupported`` copy AND fires
   ``reportUnsupportedValue({kind:"chat_failure_code"})`` — a DRIFT-DETECTION
   alarm — on a normal, correct, fail-closed path, which destroys the detector's
   ability to distinguish real drift from routine behavior (CLAUDE.md: "if
   telemetry cannot distinguish these from correct behavior, the observability
   seam is incomplete").

This test only READS the web file; the map entries and their en/fa-IR catalog
copy are owned by the web/locale surface.
"""

from __future__ import annotations

import re
from pathlib import Path

from llm.envelope.models import EMITTABLE_FAILURE_CODES

# services/llm/tests/<file> → repo root is four parents up.
_REPO_ROOT = Path(__file__).resolve().parents[3]
_SRC = _REPO_ROOT / "services" / "llm" / "src" / "llm"
_CATALOG_MAPS = _REPO_ROOT / "apps" / "web" / "src" / "chat" / "catalogMaps.ts"

# The modules that may construct a §12.4 failure. ``envelope/models.py`` DEFINES
# the type and the shared factory; the two orchestrator modules are the only
# seams that emit one. A third emitter is a deliberate, reviewed change — and it
# must be added to the scan below, not silently left unscanned.
_DEFINING_MODULE = _SRC / "envelope" / "models.py"
_EMITTING_MODULES = (
    _SRC / "orchestrator" / "graph.py",
    _SRC / "orchestrator" / "context_node.py",
)

# A failure code is a SCREAMING_SNAKE string literal; every other literal in the
# emitting modules is lowercase (state keys, reason tokens, prose).
_CODE_LITERAL = re.compile(r'"([A-Z][A-Z0-9_]*)"')
# Any construction of the §12.4 failure state.
_FAILURE_CONSTRUCTION = re.compile(r"\b(?:TurnFailure|screens_failure)\s*\(")


def _emitted_code_literals() -> set[str]:
    """Every SCREAMING_SNAKE literal in the failure-emitting modules."""
    found: set[str] = set()
    for module in _EMITTING_MODULES:
        found |= set(_CODE_LITERAL.findall(module.read_text(encoding="utf-8")))
    return found


def _failure_code_key_map() -> set[str]:
    """The keys of the web edge's CLOSED ``FAILURE_CODE_KEY`` map."""
    text = _CATALOG_MAPS.read_text(encoding="utf-8")
    block = re.search(
        r"export const FAILURE_CODE_KEY:[^{]*\{(.*?)\n\};", text, flags=re.DOTALL
    )
    assert block is not None, f"FAILURE_CODE_KEY not found in {_CATALOG_MAPS}"
    return set(re.findall(r"^\s*([A-Z][A-Z0-9_]*)\s*:", block.group(1), flags=re.MULTILINE))


def test_every_emitted_failure_code_literal_is_declared() -> None:
    """The declared set IS the set the code emits — not a copy that can drift."""
    # Non-empty guard (mirrors the TS side's ``declared.length > 0``): emptying the
    # declaration AND the emitters together must not satisfy the equality vacuously.
    assert EMITTABLE_FAILURE_CODES
    assert _emitted_code_literals() == set(EMITTABLE_FAILURE_CODES)


def test_only_the_known_modules_construct_a_turn_failure() -> None:
    """A new failure-emitting seam must join the scan above, not bypass it."""
    emitters = {
        path
        for path in _SRC.rglob("*.py")
        if _FAILURE_CONSTRUCTION.search(path.read_text(encoding="utf-8"))
    }
    unexpected = emitters - {_DEFINING_MODULE, *_EMITTING_MODULES}
    assert not unexpected, (
        "failure codes emitted outside the scanned modules: "
        f"{sorted(str(p.relative_to(_REPO_ROOT)) for p in unexpected)}"
    )


def test_web_catalog_maps_file_exists() -> None:
    assert _CATALOG_MAPS.is_file(), f"missing web edge map: {_CATALOG_MAPS}"


def test_every_emittable_failure_code_is_mapped_at_the_web_edge() -> None:
    """An emittable code missing from the closed edge map fires the drift alarm."""
    unmapped = set(EMITTABLE_FAILURE_CODES) - _failure_code_key_map()
    assert not unmapped, (
        "failure codes the LLM plane emits but the web edge does not map "
        f"(each renders chat.failure.unsupported AND fires the drift alarm): {sorted(unmapped)}"
    )
