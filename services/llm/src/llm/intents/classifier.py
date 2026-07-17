"""Small-model intent classifier (PRD §12.1: small model = intent + extraction).

The classifier normalizes the message, asks the small model for ONE of the eight
:class:`~llm.intents.models.IntentClass` values via ``response_format``, then
applies the deterministic :func:`~llm.intents.models.route_intent`. The model is
built through the single OpenAI-compatible port (``build_chat_model``); in tests
and CI it is the deterministic mock — never a paid call (§12.5). Real endpoint
selection is configuration, not a code branch (§12.1).

The classifier binds NO tools (``tools=[]``): classification itself can never
invoke a tool, and Approve/Confirm additionally route to guidance only. Two
independent guarantees, so a tool call for those intents is impossible.
"""

from __future__ import annotations

from dataclasses import dataclass
from typing import Any

from langchain.agents import create_agent
from langchain.agents.structured_output import ToolStrategy
from langchain_core.language_models.chat_models import BaseChatModel
from langchain_core.runnables import Runnable
from langgraph.errors import GraphRecursionError

from llm.intents.models import (
    IntentClass,
    IntentClassification,
    IntentRoute,
    route_intent,
)
from llm.intents.normalize import NormalizedInput, normalize_input

_INTENT_SYSTEM_PROMPT = (
    "You classify one marketplace-operator message into EXACTLY one intent class: "
    "Question, Simulation, PrepareAction, ReviewAction, ApproveAction, "
    "ConfirmResult, Administration, or Navigation. Persian, English, and mixed "
    "input and operator shorthand are all in scope. You only classify — you never "
    "approve, execute, confirm, or decide anything, and you never invoke a tool. "
    "Return the single best class and a short rationale."
)


@dataclass(frozen=True)
class IntentDecision:
    """The full result of classifying one turn.

    ``classification`` is the model's typed choice; ``route`` is the deterministic
    disposition. ``may_use_tools`` is the single gate every downstream tool
    binding must honor.
    """

    normalized: NormalizedInput
    classification: IntentClassification
    route: IntentRoute

    @property
    def intent(self) -> IntentClass:
        return self.classification.intent

    @property
    def may_use_tools(self) -> bool:
        """False for ApproveAction/ConfirmResult — they never reach a tool."""
        return self.route.may_use_tools


class IntentClassifier:
    """Classifies a turn with the small model, then routes it deterministically.

    Built once from a model + settings. The model is the OpenAI-compatible mock in
    tests; production selects a configured endpoint. Binds no tools.
    """

    # A tiny per-classification step ceiling. Classification is a single model
    # call that must return the structured class; a model that will not is a
    # failure, not something to retry into an unbounded loop (fail closed).
    _CLASSIFY_RECURSION_LIMIT = 4

    def __init__(self, model: BaseChatModel) -> None:
        # Typed as Runnable so the concrete framework graph type never leaks into
        # our surface; it is invoked, never structurally inspected in prod.
        self._agent: Runnable[Any, dict[str, Any]] = create_agent(
            model,
            tools=[],  # classification never invokes a tool.
            system_prompt=_INTENT_SYSTEM_PROMPT,
            response_format=ToolStrategy(IntentClassification),
        )

    def classify(self, message: str) -> IntentDecision:
        """Normalize, classify with the small model, then route deterministically."""
        normalized = normalize_input(message)
        classification = self._invoke(normalized.text)
        route = route_intent(classification.intent)
        return IntentDecision(
            normalized=normalized, classification=classification, route=route
        )

    def _invoke(self, text: str) -> IntentClassification:
        try:
            out: dict[str, Any] = self._agent.invoke(
                {"messages": [("user", text)]},
                {"recursion_limit": self._CLASSIFY_RECURSION_LIMIT},
            )
        except GraphRecursionError as exc:
            # The model would not produce a structured class within the ceiling —
            # fail closed rather than loop or guess (§12.4).
            raise ValueError(
                "intent classifier did not converge on a structured classification "
                "(fail closed — never guess an intent)"
            ) from exc
        structured = out.get("structured_response")
        if not isinstance(structured, IntentClassification):
            # Fail closed: an unusable classification never guesses an intent.
            raise ValueError(
                "intent classifier returned no structured IntentClassification "
                "(fail closed — never guess an intent)"
            )
        return structured
