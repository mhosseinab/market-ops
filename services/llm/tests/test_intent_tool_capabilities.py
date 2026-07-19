"""Per-intent tool-capability + binding containment tests (issue #31).

Never-cut invariant (PRD §8.2, §12.3, CLAUDE.md LLM-plane safety): ONLY
*Prepare Action* may create a Draft. The old routing contract exposed a single
``may_use_tools: bool`` that was ``True`` for six intents even though the shared
registry holds BOTH read and Draft tools — so the boolean could not express
"read-only vs Draft-only authority". These tests pin the corrected contract:

* PrepareAction is the ONLY intent that can bind any ``draft.*`` tool;
* ApproveAction / ConfirmResult bind ZERO tools (guidance only);
* Question / Simulation / ReviewAction / Administration / Navigation bind ONLY
  their explicitly-granted READ tools and NEVER a Draft tool;
* an unmapped / new intent defaults to the EMPTY capability (fail closed);
* the binding layer can never WIDEN a capability — the bound set is always a
  subset of both the intent's own capability and the registry.

Negative assertions come first (CLAUDE.md TDD: negative tests are first-class).
"""

from __future__ import annotations

import pytest
from llm.intents import IntentClass
from llm.intents.capabilities import (
    EMPTY_CAPABILITY,
    IntentCapability,
    capability_for,
)
from llm.tools.binding import bind_tools_for_intent
from llm.tools.registry import (
    DRAFT_TOOL_NAMES,
    READ_TOOL_NAMES,
    ToolKind,
    build_registry,
)

_ALL_INTENTS = tuple(IntentClass)

# The five tool-capable-but-READ-ONLY intent classes: they may consult read
# tools but must NEVER inherit Draft authority just because they can read.
_READ_ONLY_INTENTS = (
    IntentClass.QUESTION,
    IntentClass.SIMULATION,
    IntentClass.REVIEW_ACTION,
    IntentClass.ADMINISTRATION,
    IntentClass.NAVIGATION,
)

_GUIDANCE_ONLY_INTENTS = (IntentClass.APPROVE_ACTION, IntentClass.CONFIRM_RESULT)


# --- negative first: draft authority is exclusive to PrepareAction -----------


@pytest.mark.parametrize("intent", _ALL_INTENTS)
def test_only_prepare_action_can_bind_a_draft_tool(intent: IntentClass) -> None:
    """No intent other than PrepareAction may bind ANY ``draft.*`` tool."""
    registry = build_registry()
    bound = bind_tools_for_intent(intent, registry)
    if intent is IntentClass.PREPARE_ACTION:
        assert bound & DRAFT_TOOL_NAMES == DRAFT_TOOL_NAMES
    else:
        assert bound & DRAFT_TOOL_NAMES == frozenset()


@pytest.mark.parametrize("intent", _GUIDANCE_ONLY_INTENTS)
def test_approve_and_confirm_bind_no_tools_at_all(intent: IntentClass) -> None:
    """ApproveAction / ConfirmResult are guidance-only: they bind ZERO tools."""
    registry = build_registry()
    assert bind_tools_for_intent(intent, registry) == frozenset()
    assert capability_for(intent) is EMPTY_CAPABILITY
    assert capability_for(intent).tool_names == frozenset()


@pytest.mark.parametrize("intent", _READ_ONLY_INTENTS)
def test_read_only_intents_bind_only_reads_never_drafts(intent: IntentClass) -> None:
    """These intents bind ONLY their required read tools — and no Draft tool."""
    registry = build_registry()
    bound = bind_tools_for_intent(intent, registry)
    assert bound  # they are genuinely tool-capable
    assert bound <= READ_TOOL_NAMES
    assert bound & DRAFT_TOOL_NAMES == frozenset()
    # every bound name is a READ-kinded tool in the registry
    for name in bound:
        assert registry.spec(name).kind is ToolKind.READ


def test_prepare_action_binds_its_drafts_plus_required_reads() -> None:
    """PrepareAction binds all three Draft tools AND its required reads."""
    registry = build_registry()
    bound = bind_tools_for_intent(IntentClass.PREPARE_ACTION, registry)
    assert DRAFT_TOOL_NAMES <= bound
    assert bound & READ_TOOL_NAMES  # it still reads before drafting
    assert bound <= registry.names()


# --- fail-closed default for unmapped / new intents --------------------------


def test_empty_capability_grants_nothing() -> None:
    assert EMPTY_CAPABILITY.tool_names == frozenset()
    assert EMPTY_CAPABILITY.allowed_kinds == frozenset()


def test_capability_map_is_total_over_intent_class() -> None:
    """Every one of the eight classes has an explicit capability entry."""
    for intent in _ALL_INTENTS:
        assert isinstance(capability_for(intent), IntentCapability)


def test_unmapped_intent_defaults_to_empty_capability(monkeypatch: pytest.MonkeyPatch) -> None:
    """A lookup miss (a hypothetical new/unmapped intent) yields EMPTY — fail closed."""
    from llm.intents import capabilities as caps

    # Drop PrepareAction from the map to simulate an unmapped intent, then prove
    # the lookup falls back to EMPTY rather than inheriting any authority.
    pruned = dict(caps._CAPABILITIES)
    del pruned[IntentClass.PREPARE_ACTION]
    monkeypatch.setattr(caps, "_CAPABILITIES", pruned)

    assert caps.capability_for(IntentClass.PREPARE_ACTION) is EMPTY_CAPABILITY
    registry = build_registry()
    assert bind_tools_for_intent(IntentClass.PREPARE_ACTION, registry) == frozenset()


# --- the binding layer cannot widen a capability -----------------------------


@pytest.mark.parametrize("intent", _ALL_INTENTS)
def test_bound_set_is_subset_of_capability_and_registry(intent: IntentClass) -> None:
    registry = build_registry()
    bound = bind_tools_for_intent(intent, registry)
    assert bound <= capability_for(intent).tool_names
    assert bound <= registry.names()


def test_requested_selector_can_only_narrow_never_widen() -> None:
    """A ``requested`` selector intersects the intent's grant — it cannot add."""
    registry = build_registry()
    # Asking Question for a draft tool it was never granted yields nothing.
    bound = bind_tools_for_intent(
        IntentClass.QUESTION, registry, requested=frozenset({"draft_recommendation"})
    )
    assert bound == frozenset()
    # Asking for a tool the registry does not hold yields nothing.
    bound = bind_tools_for_intent(
        IntentClass.QUESTION, registry, requested=frozenset({"read_nonexistent"})
    )
    assert bound == frozenset()
    # Asking PrepareAction for a single draft returns exactly that one.
    bound = bind_tools_for_intent(
        IntentClass.PREPARE_ACTION, registry, requested=frozenset({"draft_recommendation"})
    )
    assert bound == frozenset({"draft_recommendation"})


def test_kind_gate_blocks_a_draft_even_if_the_name_is_granted(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """If a capability (wrongly) names a draft tool but does not grant the DRAFT
    kind, the binding layer's kind gate still refuses it — defense in depth."""
    from llm.intents import capabilities as caps

    # Question with a draft NAME smuggled in, but allowed_kinds still READ-only.
    smuggled = IntentCapability(
        allowed_kinds=frozenset({ToolKind.READ}),
        tool_names=frozenset({"read_catalog", "draft_recommendation"}),
    )
    patched = dict(caps._CAPABILITIES)
    patched[IntentClass.QUESTION] = smuggled
    monkeypatch.setattr(caps, "_CAPABILITIES", patched)

    registry = build_registry()
    bound = bind_tools_for_intent(IntentClass.QUESTION, registry)
    assert "draft_recommendation" not in bound
    assert bound & DRAFT_TOOL_NAMES == frozenset()
    assert "read_catalog" in bound
