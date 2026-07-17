"""Intent classes and deterministic routing (PRD §8.2, §12.3, CHAT-041).

The eight P0 intent classes and the PURE function that maps a class to a routing
disposition. Routing is deterministic and model-free: whether a class may reach
a tool is a property of the class, decided here — never by the model.

The containment invariant is structural: :data:`GUIDANCE_ONLY_INTENTS` (Approve
Action and ConfirmResult) route to GUIDANCE ONLY. :func:`route_intent` returns
``may_use_tools=False`` for them, so no downstream code can bind or invoke a tool
on their behalf. Free text never approves or confirms (§8, §12.3, CHAT-041); the
structured control that does lives outside the model plane.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field


class IntentClass(StrEnum):
    """The eight P0 conversational intent classes (PRD §8.2)."""

    QUESTION = "Question"
    SIMULATION = "Simulation"
    PREPARE_ACTION = "PrepareAction"
    REVIEW_ACTION = "ReviewAction"
    APPROVE_ACTION = "ApproveAction"
    CONFIRM_RESULT = "ConfirmResult"
    ADMINISTRATION = "Administration"
    NAVIGATION = "Navigation"


class IntentDisposition(StrEnum):
    """What a classified turn is allowed to do downstream.

    ``TOOL_CAPABLE`` — may consult read/Draft-only tools (still nothing past
    Draft). ``GUIDANCE_ONLY`` — NEVER routes to a tool; produces guidance to use
    the structured control (Approve/Confirm — §12.3, CHAT-041).
    """

    TOOL_CAPABLE = "tool_capable"
    GUIDANCE_ONLY = "guidance_only"


# The two classes that structurally never touch a tool. This frozenset is the
# single source of the containment rule; the routing function and its test both
# read it, and the registry (a separate defense) holds no approve/confirm tool at
# all — so even a misroute has nothing to call.
GUIDANCE_ONLY_INTENTS: frozenset[IntentClass] = frozenset(
    {IntentClass.APPROVE_ACTION, IntentClass.CONFIRM_RESULT}
)

# Catalog keys for the structured-control guidance shown for guidance-only
# intents (localization boundary: keys, never literals — CLAUDE.md §Localization).
_GUIDANCE_KEYS: dict[IntentClass, str] = {
    IntentClass.APPROVE_ACTION: "chat.guidance.approve_via_control",
    IntentClass.CONFIRM_RESULT: "chat.guidance.confirm_via_control",
}


class IntentClassification(BaseModel):
    """The small model's typed output (``response_format``).

    The model chooses exactly one class and gives a short rationale. It carries no
    authority and never names a tool — routing is decided deterministically from
    :attr:`intent` by :func:`route_intent`.
    """

    model_config = ConfigDict(extra="forbid")

    intent: IntentClass
    rationale: str = Field(default="", max_length=400)


@dataclass(frozen=True)
class IntentRoute:
    """The deterministic routing decision for one intent class.

    ``may_use_tools`` gates every downstream tool binding. ``guidance_key`` is the
    catalog key for the structured-control guidance shown when the disposition is
    GUIDANCE_ONLY; it is ``None`` for tool-capable intents.
    """

    intent: IntentClass
    disposition: IntentDisposition
    may_use_tools: bool
    guidance_key: str | None


def route_intent(intent: IntentClass) -> IntentRoute:
    """Map an intent class to its routing disposition. Pure and total.

    ApproveAction and ConfirmResult route to GUIDANCE_ONLY with
    ``may_use_tools=False`` — they can NEVER invoke a tool (§12.3, CHAT-041).
    Every other class is TOOL_CAPABLE (read/Draft-only; nothing past Draft).
    """
    if intent in GUIDANCE_ONLY_INTENTS:
        return IntentRoute(
            intent=intent,
            disposition=IntentDisposition.GUIDANCE_ONLY,
            may_use_tools=False,
            guidance_key=_GUIDANCE_KEYS[intent],
        )
    return IntentRoute(
        intent=intent,
        disposition=IntentDisposition.TOOL_CAPABLE,
        may_use_tools=True,
        guidance_key=None,
    )
