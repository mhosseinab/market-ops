"""Small-model intent classifier over the mock provider (PRD §12.1, §12.5).

The classifier runs on the deterministic mock — no paid call, ever. These tests
prove the model path is wired (normalize → classify → route) and that every one
of the eight classes round-trips through ``response_format``. Threshold
measurement against the corpus is S24; here we only check the seam.
"""

from __future__ import annotations

import pytest
from llm.intents import IntentClass, IntentClassifier
from llm.providers.mock import MockChatModel, MockScript


def _classifier(intent: IntentClass) -> IntentClassifier:
    return IntentClassifier(
        MockChatModel(
            script=MockScript(
                mode="answer",
                response_tool_name="IntentClassification",
                response_args={"intent": intent.value, "rationale": "mock"},
            )
        )
    )


@pytest.mark.parametrize("intent", list(IntentClass))
def test_each_class_round_trips(intent: IntentClass) -> None:
    decision = _classifier(intent).classify("قیمت ۱۲۳ SKU-1")
    assert decision.intent is intent
    # Normalization ran: Persian digits folded before classification.
    assert "123" in decision.normalized.text


def test_classifier_fails_closed_without_structured_output() -> None:
    """A model that emits no structured classification never guesses an intent."""
    classifier = IntentClassifier(MockChatModel(script=MockScript(mode="say", content="hi")))
    with pytest.raises(ValueError):
        classifier.classify("hello")
