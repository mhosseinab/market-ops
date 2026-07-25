"""``TurnState`` stays JSON-safe business data only (plan §4.8 amendment).

Graph state holds JSON-safe business data ONLY — no pydantic instances, no
framework objects, no agent handles. This is a drift guard over the state keys
themselves: a key added with a non-JSON annotation, or a node that writes a
model instance, breaks here rather than at a serialization boundary later.

Kept deliberately narrow and mechanical: it reads the declared annotations, so a
future step that adds a state key must declare a JSON-safe type for it.
"""

from __future__ import annotations

import json
import types
import typing
from typing import Any, get_args, get_origin

from llm.orchestrator.graph import TurnState

# Every key the P0 turn declares. A rename/removal is a deliberate, reviewed
# change to the graph's cross-node contract — not an incidental edit.
EXPECTED_KEYS = {
    "message",
    "organization_id",
    "marketplace_account_id",
    "conversation_id",
    "turn_context",
    "intent",
    "context_resolution",
    "active_context",
    # The deterministic S23 flow's authoritative facts (issue #108, 108c). Added
    # deliberately: the dispatcher writes it and the envelope merge reads it, so
    # it is part of the graph's cross-node contract.
    "flow_result",
    "answer",
    "failure",
}

# The closed set of JSON-safe leaf types a state value may be annotated with.
_JSON_LEAVES = {str, int, float, bool, type(None), dict, list, Any}


def _is_json_safe(annotation: Any) -> bool:  # noqa: ANN401
    origin = get_origin(annotation)
    if origin is None:
        return annotation in _JSON_LEAVES
    if origin in (dict, list, types.UnionType) or origin is typing.Union:
        args = get_args(annotation)
        # dict[str, Any] / list[str] / X | None — every argument must be a leaf.
        return all(_is_json_safe(arg) for arg in args)
    return False


def test_turn_state_declares_exactly_the_expected_keys() -> None:
    assert set(typing.get_type_hints(TurnState)) == EXPECTED_KEYS


def test_every_turn_state_value_is_json_safe() -> None:
    for name, annotation in typing.get_type_hints(TurnState).items():
        assert _is_json_safe(annotation), f"TurnState[{name}] is not JSON-safe: {annotation}"


def test_a_populated_turn_state_serializes_as_json() -> None:
    state: TurnState = {
        "message": "hi",
        "organization_id": "org-1",
        "marketplace_account_id": "acct-1",
        "conversation_id": "conv-1",
        "turn_context": {"kind": "product", "entity_id": "v1"},
        "intent": "Question",
        "context_resolution": {"kind": "resolved", "reason": "active_context"},
        "active_context": {"context_type": "Product", "entity_id": "v1"},
        "answer": {"cards": []},
        "failure": None,
    }
    json.dumps(state)
