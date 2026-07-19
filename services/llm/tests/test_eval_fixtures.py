"""Integrity of the §12.5 S21 eval seed (counts + schema, not thresholds).

S21 authors the corpus; S24 measures. Here we only assert the fixtures exist,
have the required counts (200 intent / 100 context), validate against the typed
schemas, and that the Persian/mixed cases are flagged pending native review.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from llm.contextres import ResolveRequest
from llm.intents.models import IntentClass

_EVALS = Path(__file__).resolve().parents[1] / "fixtures" / "evals"
_VALID_INTENTS = {i.value for i in IntentClass}
_VALID_KINDS = {"resolved", "picker", "not_found"}


def _load(path: Path) -> list[dict[str, Any]]:
    with path.open(encoding="utf-8") as fh:
        return [json.loads(line) for line in fh if line.strip()]


def test_intent_corpus_has_200_valid_cases() -> None:
    rows = [r for p in sorted((_EVALS / "intents").glob("*.jsonl")) for r in _load(p)]
    assert len(rows) == 200
    ids = set()
    for r in rows:
        assert r["expected_intent"] in _VALID_INTENTS, r["id"]
        assert r["lang"] in {"fa", "en", "mixed"}
        assert r["message"].strip()
        assert r["id"] not in ids  # unique ids
        ids.add(r["id"])
        if r["lang"] in {"fa", "mixed"}:
            assert r["pending_native_review"] is True


def test_intent_corpus_covers_all_eight_classes() -> None:
    rows = [r for p in sorted((_EVALS / "intents").glob("*.jsonl")) for r in _load(p)]
    seen = {r["expected_intent"] for r in rows}
    assert seen == _VALID_INTENTS


def test_context_corpus_has_100_valid_cases() -> None:
    rows = [r for p in sorted((_EVALS / "context").glob("*.jsonl")) for r in _load(p)]
    assert len(rows) == 100
    ids = set()
    for r in rows:
        assert r["expected"]["kind"] in _VALID_KINDS, r["id"]
        assert r["id"] not in ids
        ids.add(r["id"])
        # Each case maps onto a valid, typed ResolveRequest (no malformed seed),
        # carrying the authenticated organization/account scope (PRD §12).
        ResolveRequest.model_validate(
            {
                "intent": r["intent"],
                "scope": r["scope"],
                "active_context": r["active_context"],
                "references": r["references"],
                "candidates": r["candidates"],
                "time_phrase": r["time_phrase"],
                "now": r["now"],
            }
        )


def test_ambiguous_file_is_exactly_the_ambiguous_cases() -> None:
    ambiguous = _load(_EVALS / "context" / "context_ambiguous.jsonl")
    assert ambiguous
    for r in ambiguous:
        assert r["ambiguous"] is True
        assert r["expected"]["kind"] == "picker"
