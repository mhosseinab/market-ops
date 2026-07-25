"""The PRODUCTION runner seam cannot widen the registry (issue #108, 108c; §12.3).

Issue #108 wires real gateway transports behind the model-visible READ tools. The
registry is the single structural guarantee that the model plane holds no
approve / execute / confirm-result / guardrail-write / permission capability
(CHAT-003), so the new seam has to be at least as strong as the eval-only one it
sits beside (``read_runner_overrides``, issue #112) — which must keep working
untouched.

These are the negative tests, written before the seam had a consumer:

* it may never ADD a tool (so it can never introduce a forbidden one);
* it may never wire a DRAFT tool (Draft origination is the deterministic
  Prepare-Action flow's alone — hazard H2, "exactly once");
* it may never change a tool's name / kind / args schema / perm_action;
* it may never collide with the eval seam (an implicit precedence rule would let
  one silently shadow the other);
* the ``FORBIDDEN_NAME_TOKENS`` and ``_assert_contained`` guards still run.
"""

from __future__ import annotations

from typing import Any

import pytest
from llm.tools.registry import (
    DRAFT_TOOL_NAMES,
    READ_TOOL_NAMES,
    ToolKind,
    build_registry,
)


def _runner(**_kwargs: Any) -> dict[str, Any]:
    return {"status": "ok", "wired": True}


# --- the seam can never add, re-kind, or forbid-name a tool ------------------


def test_production_seam_rejects_an_unknown_tool_name() -> None:
    """A runner for a tool the registry does not hold is rejected — no ADD path."""
    with pytest.raises(ValueError):
        build_registry(production_read_runners={"read_nonexistent": _runner})


@pytest.mark.parametrize(
    "forbidden",
    [
        "approve_recommendation",
        "execute_price_change",
        "confirm_result",
        "write_guardrail",
        "grant_permission",
    ],
)
def test_production_seam_cannot_introduce_a_forbidden_tool(forbidden: str) -> None:
    """An approve/execute/confirm/guardrail/permission-shaped name has no entry point.

    The seam only ever wires a runner onto an EXISTING spec, and no such spec can
    exist (``FORBIDDEN_NAME_TOKENS`` rejects the name at ``ToolSpec`` construction),
    so the forbidden name is unknown here and fails closed.
    """
    with pytest.raises(ValueError):
        build_registry(production_read_runners={forbidden: _runner})


@pytest.mark.parametrize("draft_tool", sorted(DRAFT_TOOL_NAMES))
def test_production_seam_rejects_every_draft_tool(draft_tool: str) -> None:
    """A DRAFT tool keeps its fail-closed stub in production (§8.2, hazard H2).

    This is what makes "Prepare Action reaches the real Draft endpoint EXACTLY
    ONCE" structural rather than incidental: the model-visible ``draft_*`` tools
    have no real transport at all, so the deterministic flow is the only path to
    a Draft-create.
    """
    with pytest.raises(ValueError):
        build_registry(production_read_runners={draft_tool: _runner})


def test_production_seam_does_not_change_any_spec() -> None:
    """Wiring a runner substitutes BEHAVIOUR only — never the declared contract."""
    baseline = {
        spec.name: (spec.kind, spec.perm_action, spec.args_schema, spec.description)
        for spec in build_registry().specs()
    }
    wired = build_registry(
        production_read_runners={name: _runner for name in READ_TOOL_NAMES}
    )
    assert {
        spec.name: (spec.kind, spec.perm_action, spec.args_schema, spec.description)
        for spec in wired.specs()
    } == baseline
    # And the containment invariant still holds over the wired registry.
    assert all(spec.kind in (ToolKind.READ, ToolKind.DRAFT) for spec in wired.specs())
    assert wired.names() == READ_TOOL_NAMES | DRAFT_TOOL_NAMES


def test_production_seam_and_eval_seam_must_be_disjoint() -> None:
    """The same tool wired by both seams is a wiring bug, not a precedence rule."""
    with pytest.raises(ValueError):
        build_registry(
            read_runner_overrides={"read_margin": _runner},
            production_read_runners={"read_margin": _runner},
        )


# --- the eval-only seam (#112) keeps working EXACTLY as it did ---------------


def test_eval_override_seam_still_substitutes_read_data() -> None:
    """Regression guard: #112's eval seam is unchanged by the new production seam."""
    registry = build_registry(
        read_runner_overrides={"read_margin": lambda **_k: {"status": "faked"}}
    )
    result = registry.tool("read_margin").invoke(
        {"marketplace_account_id": "acct-1", "entity_id": "sku-1"}
    )
    assert result == {"status": "faked"}


def test_eval_override_seam_still_rejects_a_draft_tool() -> None:
    with pytest.raises(ValueError):
        build_registry(read_runner_overrides={"draft_recommendation": _runner})


def test_unwired_tools_keep_the_fail_closed_stub() -> None:
    """A tool the production seam does not name still fails closed, not silently."""
    registry = build_registry(production_read_runners={"read_action": _runner})
    stubbed = registry.tool("draft_recommendation").invoke(
        {"marketplace_account_id": "acct-1", "recommendation_id": "rec-1"}
    )
    assert stubbed["status"] == "unavailable"
