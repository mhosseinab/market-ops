"""Intent ROUTING tests — the pure class→disposition map (PRD §8.2, §12.3).

Scope discipline (issue #33): the tests in this file prove the *routing* half of
containment — that GIVEN an intent class, the deterministic route forbids or
permits tools correctly. Several of them drive a mock whose intent label is
PRESET (``_classifier_for``): they therefore prove "a turn ALREADY labelled
ApproveAction/ConfirmResult never routes to a tool", NOT that the real classifier
assigns that safe label to adversarial input. Their names say so explicitly, so
the evidence is not overstated.

The complementary half — raw adversarial messages traversing the ACTUAL
content-sensitive classifier with no preset label, and the EFFECTIVE downstream
tool scope that results — lives in ``test_intent_effective_scope.py`` (issue #33)
and ``test_adversarial_approval.py`` (the live-turn corpus). Together the three
files cover: (1) routing is total and correct per class; (2) a preset safe label
never routes to a tool; (3) raw adversarial text through the real classifier
never yields Draft/approve capability.

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
    """A classifier whose mock ALWAYS returns the PRESET label ``intent``.

    The label is fixed regardless of the message, so any test built on this helper
    exercises the routing/binding path for a *known* class — never the classifier's
    ability to assign that class from raw text. Tests that need the real
    content-sensitive classification use the keyword mock (see
    ``test_intent_effective_scope.py``), not this helper.
    """
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
def test_preset_guidance_only_label_never_routes_to_a_tool(intent: IntentClass) -> None:
    """A turn PRESET-labelled Approve/Confirm routes to guidance, never a tool.

    This is a routing/binding assertion, not a classification one: the mock is
    forced to emit ``intent`` irrespective of the message. It proves the route
    for that class is guidance-only — the real-classifier boundary is covered in
    ``test_intent_effective_scope.py``.
    """
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
    cannot invoke one (belt-and-braces with the routing containment). Independent
    of the preset label, so this is a true structural guarantee.
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
def test_preset_guidance_only_label_forbids_tools_for_any_message(
    adversarial: str,
) -> None:
    """With the label PRESET to Approve/Confirm, ANY message routes tool-free.

    Named to state its actual scope (issue #33): it does NOT test whether the
    classifier assigns the guidance-only label to these strings — that is the job
    of ``test_intent_effective_scope.py``, which runs them through the real
    content-sensitive classifier with no preset. Here the label is forced, so the
    only thing proven is that the routing for that label forbids tools.
    """
    for intent in (IntentClass.APPROVE_ACTION, IntentClass.CONFIRM_RESULT):
        decision = _classifier_for(intent).classify(adversarial)
        assert decision.may_use_tools is False


def test_preset_tool_capable_labels_permit_tools() -> None:
    """With the label PRESET to a tool-capable class, routing permits tools.

    A routing assertion over a forced label (not classification) — the mirror of
    the guidance-only preset test above.
    """
    for intent in (
        IntentClass.QUESTION,
        IntentClass.SIMULATION,
        IntentClass.PREPARE_ACTION,
        IntentClass.REVIEW_ACTION,
        IntentClass.ADMINISTRATION,
        IntentClass.NAVIGATION,
    ):
        assert _classifier_for(intent).classify("qقیمت").may_use_tools is True
