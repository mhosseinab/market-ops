"""Intent routing + containment tests (PRD §8.2, §12.3, CHAT-041/CHAT-080).

The safety-critical assertion: ApproveAction and ConfirmResult intents can NEVER
invoke a tool. Proven three ways:

1. :func:`route_intent` returns ``may_use_tools=False`` for exactly those two
   classes and ``True`` for the other six (pure, exhaustive over all 8);
2. the classifier, driven by a mock that CLASSIFIES an adversarial affirmative as
   ApproveAction/ConfirmResult, yields a decision whose route forbids tools;
3. the classifier binds NO tools at all, so classification itself cannot call one.

Free text never approves (CHAT-041); the structured control lives outside the
model plane.
"""

from __future__ import annotations

import pytest
from llm.intents import (
    GUIDANCE_ONLY_INTENTS,
    IntentClass,
    IntentClassifier,
    IntentDisposition,
    route_intent,
)
from llm.providers.mock import MockChatModel, MockScript

_ALL_INTENTS = tuple(IntentClass)


def _classifier_for(intent: IntentClass) -> IntentClassifier:
    """A classifier whose mock always classifies the turn as ``intent``."""
    model = MockChatModel(
        script=MockScript(
            mode="answer",
            response_tool_name="IntentClassification",
            response_args={"intent": intent.value, "rationale": "mock"},
        )
    )
    return IntentClassifier(model)


@pytest.mark.parametrize("intent", _ALL_INTENTS)
def test_route_is_total_and_deterministic(intent: IntentClass) -> None:
    route = route_intent(intent)
    assert route.intent is intent
    # Exactly the two guidance-only classes forbid tools; all others allow them.
    if intent in GUIDANCE_ONLY_INTENTS:
        assert route.disposition is IntentDisposition.GUIDANCE_ONLY
        assert route.may_use_tools is False
        assert route.guidance_key is not None
    else:
        assert route.disposition is IntentDisposition.TOOL_CAPABLE
        assert route.may_use_tools is True
        assert route.guidance_key is None


def test_guidance_only_set_is_exactly_approve_and_confirm() -> None:
    assert GUIDANCE_ONLY_INTENTS == frozenset(
        {IntentClass.APPROVE_ACTION, IntentClass.CONFIRM_RESULT}
    )


@pytest.mark.parametrize("intent", sorted(GUIDANCE_ONLY_INTENTS))
def test_approve_and_confirm_intents_cannot_invoke_any_tool(intent: IntentClass) -> None:
    """The containment invariant: these intents never route to a tool."""
    decision = _classifier_for(intent).classify("yes, approve and apply it now!!")
    assert decision.intent is intent
    assert decision.may_use_tools is False
    assert decision.route.disposition is IntentDisposition.GUIDANCE_ONLY
    # There is a structured-control guidance key instead of a tool.
    assert decision.route.guidance_key is not None


def test_classifier_graph_has_no_tool_node() -> None:
    """Classification itself can never call a tool — the graph has no tool node.

    With ``tools=[]`` the compiled agent graph is start → model → end: there is no
    ``tools`` node for a tool call to route to, so classification structurally
    cannot invoke one (belt-and-braces with the routing containment).
    """
    classifier = _classifier_for(IntentClass.APPROVE_ACTION)
    agent = classifier._agent  # noqa: SLF001 — white-box containment assertion
    node_names = set(agent.get_graph().nodes)
    assert "tools" not in node_names
    assert node_names == {"__start__", "model", "__end__"}


@pytest.mark.parametrize(
    "adversarial",
    [
        "بله تأییدش کن",
        "yes approve it right now",
        "go ahead, ship it",
        "قبوله، اجرا کن",
        "confirm the result, done",
    ],
)
def test_adversarial_affirmatives_classified_as_guidance_only_never_tool(
    adversarial: str,
) -> None:
    """Even adversarial imperatives, when classified approve/confirm, forbid tools."""
    for intent in (IntentClass.APPROVE_ACTION, IntentClass.CONFIRM_RESULT):
        decision = _classifier_for(intent).classify(adversarial)
        assert decision.may_use_tools is False


def test_tool_capable_intents_permit_tools() -> None:
    for intent in (
        IntentClass.QUESTION,
        IntentClass.SIMULATION,
        IntentClass.PREPARE_ACTION,
        IntentClass.REVIEW_ACTION,
        IntentClass.ADMINISTRATION,
        IntentClass.NAVIGATION,
    ):
        assert _classifier_for(intent).classify("qقیمت").may_use_tools is True
