"""Flow containment gate (PRD §8.2, §12.3, CHAT-041). The free-text firewall.

Before any flow runs, a turn passes through :func:`contain`. For the two
guidance-only intents — ApproveAction and ConfirmResult — it returns a
:class:`~llm.flows.models.GuidanceOnly` outcome pointing at the external
structured control and records NO transition: free text never approves, executes,
or confirms (§12.3, CHAT-041). For every other intent it returns ``None``,
meaning "a tool-capable flow may proceed" — and even then the only write any flow
can originate is a Draft, because :class:`~llm.flows.ports.DraftPort` has no other
method and the registry holds no approve/execute/confirm tool.

The :class:`TransitionLedger` is the audit record every flow appends to; the
adversarial suite asserts it holds nothing but Draft transitions across a large
adversarial affirmative/imperative corpus, and NOTHING for guidance-only intents.
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field

from llm.flows.models import GuidanceOnly, TransitionKind
from llm.intents.models import IntentClass, IntentDisposition, route_intent

# Deep link to the external structured control that CAN approve/confirm — the
# same endpoint the screens use. Chat only points at it; it never owns it.
STRUCTURED_CONTROL_DEEP_LINK = "/app/screens/approve"


class TransitionLedger(BaseModel):
    """An append-only record of the transitions a conversation originated.

    The plane can only ever append :attr:`TransitionKind.DRAFT`; there is no
    approve/execute/confirm member to append. The adversarial suite reads this to
    prove ZERO approval transitions.
    """

    model_config = ConfigDict(extra="forbid")

    transitions: list[TransitionKind] = Field(default_factory=list)

    def record(self, kind: TransitionKind) -> None:
        self.transitions.append(kind)

    def approval_transitions(self) -> list[TransitionKind]:
        """Transitions that are anything other than a Draft — always empty."""
        return [t for t in self.transitions if t is not TransitionKind.DRAFT]


def contain(intent: IntentClass) -> GuidanceOnly | None:
    """Gate a turn by intent. Returns guidance for guidance-only intents.

    ApproveAction / ConfirmResult ⇒ :class:`GuidanceOnly` (no transition, ever).
    Any other intent ⇒ ``None`` (a tool-capable flow may proceed, still bounded
    to Draft). This is the single deterministic containment decision — the model
    never overrides it.
    """
    route = route_intent(intent)
    if route.disposition is IntentDisposition.GUIDANCE_ONLY:
        assert route.guidance_key is not None  # guidance-only always names a key
        return GuidanceOnly(
            guidance_key=route.guidance_key,
            deep_link=STRUCTURED_CONTROL_DEEP_LINK,
        )
    return None
