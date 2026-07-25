"""The authoritative READ port (PRD §12.1, §12.2, §19.3) — issue #108, 108c.

:class:`ReadPort` is the dependency-inversion seam for authoritative reads, the
exact counterpart of :mod:`llm.flows.ports` (the Draft-only write port): a flow
depends on this Protocol and NEVER imports an HTTP client, so the deterministic
flows stay pure and table-testable while the transport is swappable.

Its methods are exclusively READS. There is deliberately no create / draft /
approve / execute / confirm / guardrail-write / permission method — the model
plane cannot call what does not exist (§12.3, CHAT-003). The one write surface
the plane has is :class:`~llm.flows.ports.DraftPort`, and it is separate on
purpose.

**Money never becomes a float.** Every monetary field on these models is
:class:`~llm.envelope.models.Money`, decoded from the gateway's signed-decimal
STRING mantissa (``^-?[0-9]+$``, #73 / §15.1) into an exact integer and
re-serialized to the same string wire form. No ``float(...)`` appears on any path
in this module or its transport (§9.1, never-cut).

**Fail closed, never fabricate.** A transport error, a non-2xx, a malformed body,
a missing required field, or a payload whose tenant does not match the turn's
authoritative scope raises :class:`ReadUnavailable`. The turn then degrades to
the §12.4 structured failure + screens deep link; it never proceeds on a partial
or invented read.
"""

from __future__ import annotations

from collections.abc import Sequence
from typing import Protocol

from pydantic import BaseModel, ConfigDict, Field

from llm.envelope.models import Money
from llm.orchestrator.scope import TurnScope

__all__ = [
    "ActionRowRead",
    "ActionsRead",
    "BriefingEventRead",
    "BriefingRead",
    "GuardrailsRead",
    "MarginReadinessRead",
    "NoReadPort",
    "ReadPort",
    "ReadUnavailable",
    "RecommendationDetailRead",
]


class ReadUnavailable(Exception):
    """An authoritative read could not be completed — fail closed (§12.4).

    Raised on a transport error, a non-2xx response, a malformed/short body, or a
    tenant mismatch between the payload and the turn's authoritative scope. It is
    NEVER swallowed into a default that downstream code reads as success: the
    caller converts it to the existing §12.4 structured failure with the screens
    deep link, and no answer, card, or Draft is produced.
    """


# --- typed read results (JSON-safe, money-exact) -----------------------------


class BriefingEventRead(BaseModel):
    """One ranked event of the stored daily briefing (gateway ``BriefingEvent``).

    ``rank`` is 1-based and ``event_id`` matches the Today feed event id. The
    ORDER of the containing list is authoritative (CHAT-010) and is preserved
    verbatim — the plane never re-ranks, filters, or drops an item.
    """

    model_config = ConfigDict(extra="forbid")

    rank: int
    event_id: str
    event_type: str
    severity: str


class BriefingRead(BaseModel):
    """The stored once-per-business-day briefing (gateway ``DailyBriefing``)."""

    model_config = ConfigDict(extra="forbid")

    marketplace_account_id: str
    business_day: str
    generated_at: str
    events: list[BriefingEventRead] = Field(default_factory=list)

    def event_ids(self) -> list[str]:
        """The event ids in authoritative ranked order — the CHAT-010 target."""
        return [event.event_id for event in self.events]


class MarginReadinessRead(BaseModel):
    """A SKU's derived margin readiness (gateway ``MarginReadiness``, CST-003).

    ``state`` is one of the four closed states; the component lists are the
    cost-readiness blockers, relayed verbatim in the order the engine returned
    them. The plane never re-derives readiness — it only relays it.
    """

    model_config = ConfigDict(extra="forbid")

    variant_id: str
    marketplace_account_id: str
    state: str
    missing_components: list[str] = Field(default_factory=list)
    stale_components: list[str] = Field(default_factory=list)
    computed_at: str


class PolicyBlockerRead(BaseModel):
    """One typed policy blocker (gateway ``PolicyBlocker``), in engine order.

    ``stage_order`` is the engine's own numeric precedence (0 = boundary …
    5 = objective, §9.3). Ordering is taken from the engine, never re-derived
    here — a divergence would be a CHAT-070 byte-match breach.
    """

    model_config = ConfigDict(extra="forbid")

    stage: str
    stage_order: int
    code: str


class RecommendationDetailRead(BaseModel):
    """One recommendation's full PRC-001 record (gateway ``RecommendationDetail``).

    Carries the authoritative tenant (``marketplace_account_id``) the caller
    validates against the turn's scope, the target ``variant_id`` and the
    recommendation ``id`` a Prepare-Action Draft is minted from, the engine money
    figures, the evidence provenance, and the blockers in policy order.
    """

    model_config = ConfigDict(extra="forbid")

    id: str
    marketplace_account_id: str
    variant_id: str
    version: int
    objective: str
    readiness: str
    evidence_quality: str
    approvable: bool
    simulation: bool
    current_price: Money
    proposed_price: Money | None = None
    current_contribution: Money | None = None
    proposed_contribution: Money | None = None
    evidence_observation_id: str | None = None
    evidence_as_of: str | None = None
    blockers: list[PolicyBlockerRead] = Field(default_factory=list)

    def money_amounts(self) -> list[Money]:
        """Every engine money figure on the record, in a stable declared order."""
        ordered = (
            self.current_price,
            self.proposed_price,
            self.current_contribution,
            self.proposed_contribution,
        )
        return [amount for amount in ordered if amount is not None]


class ActionRowRead(BaseModel):
    """One row of the account's actions queue (gateway ``ActionSummary``).

    Read-only projection of the append-only action state history: the action id,
    its §8.4 state, and the variant it targets. It carries NO approval control —
    confirmation lives on the structured screen, outside this plane.
    """

    model_config = ConfigDict(extra="forbid")

    action_id: str
    state: str
    entity_id: str | None = None
    price: Money | None = None


class ActionsRead(BaseModel):
    """One BOUNDED page of the actions queue (gateway ``ActionList``, §17).

    ``has_more`` carries the page's truthful completeness: a partial page is
    never presented as the whole queue.
    """

    model_config = ConfigDict(extra="forbid")

    actions: list[ActionRowRead] = Field(default_factory=list)
    has_more: bool = False

    def action_ids(self) -> list[str]:
        """The action ids in the authoritative order the gateway returned."""
        return [action.action_id for action in self.actions]


class GuardrailsRead(BaseModel):
    """An account's persisted L3 commercial guardrails (``GuardrailConfigView``).

    READING guardrails is Level 1 (every role). This plane has no way to WRITE
    them: there is no method here and no Level-3 write tool in the registry
    (CHAT-062), so an administration turn can only explain and deep-link.
    """

    model_config = ConfigDict(extra="forbid")

    marketplace_account_id: str
    version: int
    updated_at: str
    settings: dict[str, object] = Field(default_factory=dict)


# --- the port ----------------------------------------------------------------


class ReadPort(Protocol):
    """Authoritative reads, every one scoped by the turn's AUTHORITATIVE scope.

    Every method takes the :class:`~llm.orchestrator.scope.TurnScope` explicitly —
    it is never derived from a model-authored argument and never defaulted. A
    caller that has no authoritative scope cannot call this port at all, which is
    the point: there is no shape of this interface in which a cross-tenant read is
    expressible.
    """

    def briefing(self, scope: TurnScope, *, business_day: str) -> BriefingRead:
        """The stored daily briefing for the scope's account (CHAT-010)."""
        ...

    def margin_readiness(self, scope: TurnScope, *, variant_id: str) -> MarginReadinessRead:
        """A SKU's derived margin readiness (CST-003). Relayed, never re-derived."""
        ...

    def recommendation_detail(
        self, scope: TurnScope, *, recommendation_id: str
    ) -> RecommendationDetailRead:
        """One recommendation's full PRC-001 record, tenant-checked against ``scope``."""
        ...

    def actions(self, scope: TurnScope) -> ActionsRead:
        """One bounded page of the account's actions queue (read-only, CHAT-073)."""
        ...

    def guardrails(self, scope: TurnScope) -> GuardrailsRead:
        """The account's L3 commercial guardrails — a READ (explanation only)."""
        ...


class NoReadPort:
    """The fail-closed default: no authoritative read transport is configured.

    **Explicitly-planned stub (CLAUDE.md Engineering method).** It supplies
    NOTHING and fabricates nothing: every method raises
    :class:`ReadUnavailable`. It is the production implementation only when the
    outbound gateway configuration is ABSENT, which the production
    (``openai_compatible``) transport refuses to start without
    (``Settings.validate_gateway_read_config``) — so in production it is reachable
    only under the deterministic mock/dev transport, where there is no gateway to
    read.

    Its negative test (``tests/test_gateway_read.py``) proves every method raises
    rather than returning an empty-but-successful read, so a caller can never
    mistake "not wired" for "nothing to report".
    """

    def briefing(self, scope: TurnScope, *, business_day: str) -> BriefingRead:
        raise ReadUnavailable(_UNCONFIGURED)

    def margin_readiness(self, scope: TurnScope, *, variant_id: str) -> MarginReadinessRead:
        raise ReadUnavailable(_UNCONFIGURED)

    def recommendation_detail(
        self, scope: TurnScope, *, recommendation_id: str
    ) -> RecommendationDetailRead:
        raise ReadUnavailable(_UNCONFIGURED)

    def actions(self, scope: TurnScope) -> ActionsRead:
        raise ReadUnavailable(_UNCONFIGURED)

    def guardrails(self, scope: TurnScope) -> GuardrailsRead:
        raise ReadUnavailable(_UNCONFIGURED)


_UNCONFIGURED = (
    "no outbound gateway read transport is configured; the read fails closed "
    "rather than returning an empty result that would read as 'nothing to report'"
)


def read_port_methods() -> Sequence[str]:
    """The read method names — used by the containment test over this Protocol."""
    return (
        "briefing",
        "margin_readiness",
        "recommendation_detail",
        "actions",
        "guardrails",
    )
