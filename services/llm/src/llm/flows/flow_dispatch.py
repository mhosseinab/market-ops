"""The deterministic eight-class flow dispatcher (issue #108, sub-scope 108c).

S23 landed six deterministic chat flows as a LIBRARY; nothing dispatched a real
turn into them. This module is that missing seam: given a CONTAINED, CONTEXT-
RESOLVED turn it decides — deterministically, never by asking the model — which
S23 flow runs, performs that flow's authoritative reads through
:class:`~llm.flows.read_ports.ReadPort`, and returns the load-bearing facts as
JSON-safe :class:`FlowFacts`.

**The dispatch contract (PD-4, binding).** All eight classifier classes, with no
dedicated blocker class — blocker resolution is a COMPOSITE deterministic entry:

===========================  ==========================================
classified intent            flow
===========================  ==========================================
Question, no entity chip     briefing (+ account-scope blockers, CHAT-070 order)
Question, entity chip        investigation (+ that entity's blockers, CHAT-070)
Simulation                   simulation — non-executable by construction
PrepareAction                prepare-action — the ONLY class with Draft capability;
                             a BLOCKED target returns blocker guidance (one at a
                             time, CHAT-071) instead of a refused Draft
ReviewAction                 monitoring — read-only action state (CHAT-073/074)
ApproveAction / ConfirmResult  never reach here (contained upstream, unchanged)
Administration               Level-1 read + explanation + Settings deep link;
                             Level 3 stays explanation + deep link (CHAT-062)
Navigation                   deterministic deep-link resolution from the closed
                             route map — no card, no Draft, no read
===========================  ==========================================

**Why the facts are separated from the prose (hazard H3).** Every load-bearing
value — ordering, ids, counts, Money, blocker order — is produced HERE from the
authoritative read and carried on :class:`FlowFacts`. The model composes free
text AROUND them and its own numeric slots are DISCARDED at the merge
(:func:`apply_flow_facts`). The model therefore cannot author, re-derive, round,
or re-order an authoritative value: byte-match is structural, not a convention.

**Why this module raises no failure (§12.4).** It returns a
:class:`FlowOutcome` carrying a machine reason token; the orchestrator owns the
single structured-failure factory, so there is exactly one failure shape and one
scanned emitter. This module never constructs a ``TurnFailure``.

**Draft origination (hazard H2).** ``PrepareAction`` is the only branch that
touches :class:`~llm.flows.ports.DraftPort`, it calls it at most ONCE per turn,
and it is unreachable for any other intent. The model-visible ``draft_*`` tools
have no production transport at all (the registry's production seam refuses a
DRAFT tool), so a turn cannot produce a second Draft even if the model tried.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from enum import StrEnum
from typing import Any

from pydantic import BaseModel, ConfigDict, Field

from llm.envelope.models import Money
from llm.flows.actions import prepare_action
from llm.flows.blockers import (
    COST_COMPONENT_ORDER,
    Blocker,
    next_blocker,
    order_cost_blockers,
)
from llm.flows.deep_links import (
    ROUTE_ACTIONS,
    ROUTE_MARKET,
    ROUTE_OPERATIONS,
    ROUTE_PRODUCTS,
    ROUTE_SETTINGS,
    ROUTE_TODAY,
    cost_entry,
    level3_explanation,
)
from llm.flows.models import DraftTicket, TransitionKind
from llm.flows.ports import DraftPort
from llm.flows.read_ports import (
    PolicyBlockerRead,
    ReadPort,
    ReadUnavailable,
    RecommendationDetailRead,
)
from llm.intents.models import IntentClass
from llm.orchestrator.scope import TurnScope

__all__ = [
    "FLOW_DISPATCH_METRIC",
    "FlowFacts",
    "FlowKind",
    "FlowOutcome",
    "apply_flow_facts",
    "dispatch_flow",
    "flow_for",
]

# Stable, locale-neutral telemetry identifier for the dispatch boundary. Every
# turn emits its flow + outcome + machine reason, so a fail-closed degradation is
# distinguishable from a correct dispatch (CLAUDE.md observability). No message
# text, no tenant identifier, no entity id and no Persian copy is recorded.
FLOW_DISPATCH_METRIC = "llm_flow_dispatch_total"
_LOGGER = logging.getLogger("llm.flows.dispatch")

# Machine reason tokens (never user-facing copy).
REASON_OK = "ok"
REASON_READ_UNAVAILABLE = "read_unavailable"
REASON_NO_SUBJECT = "no_resolved_subject"
REASON_UNSUPPORTED_SUBJECT = "unsupported_subject_kind"
REASON_NO_DRAFT_PORT = "no_draft_port"
REASON_DRAFT_UNAVAILABLE = "draft_unavailable"
REASON_BLOCKED_TARGET = "blocked_target"
REASON_NOT_DISPATCHED = "not_dispatched"

# The subject kinds this sub-scope can serve an authoritative entity read for.
# ``product`` reads margin readiness; ``recommendation`` reads the PRC-001 record.
_PRODUCT_KINDS = frozenset({"Product"})
_RECOMMENDATION_KINDS = frozenset({"Recommendation"})
# Account-scope chips: a Question bound to one of these is an ACCOUNT question,
# so it briefs rather than investigating a non-existent entity.
_ACCOUNT_KINDS = frozenset({"GlobalAccount", "Settings", "Operations"})

# The closed Navigation route map (design/IA_AND_COMPONENTS.md deep-link map),
# keyed by the chip kind the turn resolved. Navigation resolves a target from
# THIS map alone — never a model-authored path (issue #56 keeps failure links in
# a closed set for the same reason).
_NAVIGATION_ROUTES: dict[str, str] = {
    "GlobalAccount": ROUTE_TODAY,
    "Product": ROUTE_PRODUCTS,
    "MarketEvent": ROUTE_MARKET,
    "Recommendation": ROUTE_ACTIONS,
    "BulkSelection": ROUTE_ACTIONS,
    "ActionExecution": ROUTE_ACTIONS,
    "Settings": ROUTE_SETTINGS,
    "Operations": ROUTE_OPERATIONS,
}


class FlowKind(StrEnum):
    """The deterministic flow a contained, resolved turn dispatches into."""

    BRIEFING = "briefing"
    INVESTIGATION = "investigation"
    SIMULATION = "simulation"
    PREPARE_ACTION = "prepare_action"
    MONITORING = "monitoring"
    ADMINISTRATION = "administration"
    NAVIGATION = "navigation"
    # Not dispatched: the guidance-only classes never reach this node (they are
    # contained upstream) and an unclassified turn already failed closed.
    NONE = "none"


class FlowBlocker(BaseModel):
    """One blocker surfaced by a flow, in the engine's own CHAT-070 order.

    ``code`` and ``stage`` are the engine's machine tokens, relayed verbatim; the
    surface localizes them. ``order`` is the engine's numeric precedence when it
    supplied one. Nothing here is re-derived by this plane.
    """

    model_config = ConfigDict(extra="forbid")

    code: str
    stage: str
    order: int
    cost_entry_deep_link: str | None = None


class FlowFacts(BaseModel):
    """The load-bearing, AUTHORITATIVE facts one dispatched flow produced.

    Every field here comes from a deterministic read or a deterministic flow —
    never from the model. The merge (:func:`apply_flow_facts`) writes these over
    the model's answer, so the model cannot author, re-derive, round, or re-order
    any of them (hazard H3).

    JSON-safe throughout: it is dumped onto graph state, which holds business data
    only (plan §4.8).
    """

    model_config = ConfigDict(extra="forbid")

    flow: str
    # Briefing / investigation ordering — byte-equal to the authoritative read.
    event_ids: list[str] = Field(default_factory=list)
    # Monitoring: action ids grouped by their §8.4 state, in read order.
    action_groups: dict[str, list[str]] = Field(default_factory=dict)
    # Blockers in engine order; ``next_blocker_code`` is the ONE handled now.
    blockers: list[FlowBlocker] = Field(default_factory=list)
    next_blocker_code: str | None = None
    # Engine money figures, relayed in exact integer form (never a float).
    amounts: list[Money] = Field(default_factory=list)
    # Canonical state / readiness / quality tokens relayed from the engines.
    state_keys: list[str] = Field(default_factory=list)
    # The deterministic screen this turn points at (closed route map only).
    deep_link: str | None = None
    # True only for the simulation flow — a simulation is never approvable.
    simulation: bool = False
    # The single Draft a Prepare-Action turn originated, if any.
    draft: DraftTicket | None = None
    # The append-only transition ledger for this turn. It can only ever hold
    # ``draft_created``; there is no approve/execute/confirm member to hold.
    transitions: list[TransitionKind] = Field(default_factory=list)


@dataclass
class FlowOutcome:
    """What the dispatcher decided for one turn.

    ``facts`` is set when the flow produced authoritative facts. ``reason`` is
    always a stable machine token. ``failed`` means the turn must fail closed to
    the §12.4 structured failure the ORCHESTRATOR builds — this module never
    constructs one itself.
    """

    flow: FlowKind
    facts: FlowFacts | None = None
    reason: str = REASON_OK
    failed: bool = False


def flow_for(intent: IntentClass, *, subject_kind: str | None) -> FlowKind:
    """Map a contained intent + resolved subject onto its flow. Pure and total.

    Deterministic: the model never chooses. ``subject_kind`` is the resolved
    chip's ``context_type`` (or ``None`` when the turn is account-scoped) — it is
    taken from the DETERMINISTIC resolver's output, never guessed.
    """
    if intent is IntentClass.QUESTION:
        entity_bound = subject_kind is not None and subject_kind not in _ACCOUNT_KINDS
        return FlowKind.INVESTIGATION if entity_bound else FlowKind.BRIEFING
    if intent is IntentClass.SIMULATION:
        return FlowKind.SIMULATION
    if intent is IntentClass.PREPARE_ACTION:
        return FlowKind.PREPARE_ACTION
    if intent is IntentClass.REVIEW_ACTION:
        return FlowKind.MONITORING
    if intent is IntentClass.ADMINISTRATION:
        return FlowKind.ADMINISTRATION
    if intent is IntentClass.NAVIGATION:
        return FlowKind.NAVIGATION
    # ApproveAction / ConfirmResult are contained upstream and never arrive here;
    # an unmapped future class falls through to NONE — fail closed, never a guess.
    return FlowKind.NONE


def dispatch_flow(
    intent: IntentClass,
    *,
    scope: TurnScope,
    subject_kind: str | None,
    business_day: str,
    read_port: ReadPort,
    draft_port: DraftPort | None,
) -> FlowOutcome:
    """Run the deterministic flow for one contained, resolved turn.

    Every authoritative value is read here; the model contributes nothing to this
    function's output. A read that cannot be served fails the turn CLOSED — no
    fabricated answer, no card, and (for Prepare Action) no Draft.
    """
    flow = flow_for(intent, subject_kind=subject_kind)
    outcome = _run(
        flow,
        scope=scope,
        subject_kind=subject_kind,
        business_day=business_day,
        read_port=read_port,
        draft_port=draft_port,
    )
    _emit(outcome)
    return outcome


def _run(
    flow: FlowKind,
    *,
    scope: TurnScope,
    subject_kind: str | None,
    business_day: str,
    read_port: ReadPort,
    draft_port: DraftPort | None,
) -> FlowOutcome:
    if flow is FlowKind.NONE:
        return FlowOutcome(flow=flow, reason=REASON_NOT_DISPATCHED)
    if flow is FlowKind.NAVIGATION:
        return _navigation(subject_kind)
    try:
        if flow is FlowKind.BRIEFING:
            return _briefing(scope, business_day, read_port)
        if flow is FlowKind.INVESTIGATION:
            return _investigation(scope, subject_kind, read_port)
        if flow is FlowKind.SIMULATION:
            return _simulation(scope, subject_kind, read_port)
        if flow is FlowKind.MONITORING:
            return _monitoring(scope, read_port)
        if flow is FlowKind.ADMINISTRATION:
            return _administration(scope, read_port)
        return _prepare_action(scope, subject_kind, read_port, draft_port)
    except ReadUnavailable:
        # Transport error, non-2xx, malformed body, missing field, or a tenant
        # mismatch. Fail the TURN closed: no fabricated answer, no Draft (§12.4).
        return FlowOutcome(flow=flow, reason=REASON_READ_UNAVAILABLE, failed=True)


# --- the flows ---------------------------------------------------------------


def _briefing(scope: TurnScope, business_day: str, read_port: ReadPort) -> FlowOutcome:
    """Account-scope briefing (Journey 7, CHAT-010/011) + the account blocker list.

    The event ids and their ORDER are the authoritative read's, preserved
    byte-for-byte: the briefing never re-ranks, filters, or drops an item, so the
    screen, the chat briefing, and the S19 email stay one system.
    """
    briefing = read_port.briefing(scope, business_day=business_day)
    facts = FlowFacts(
        flow=FlowKind.BRIEFING.value,
        event_ids=briefing.event_ids(),
        deep_link=ROUTE_TODAY,
    )
    return FlowOutcome(flow=FlowKind.BRIEFING, facts=facts)


def _investigation(
    scope: TurnScope, subject_kind: str | None, read_port: ReadPort
) -> FlowOutcome:
    """Entity investigation (CHAT-033) + that entity's blockers in CHAT-070 order."""
    if scope.entity_id is None:
        return FlowOutcome(
            flow=FlowKind.INVESTIGATION, reason=REASON_NO_SUBJECT, failed=True
        )
    if subject_kind in _RECOMMENDATION_KINDS:
        detail = read_port.recommendation_detail(scope, recommendation_id=scope.entity_id)
        return FlowOutcome(
            flow=FlowKind.INVESTIGATION, facts=_detail_facts(FlowKind.INVESTIGATION, detail)
        )
    if subject_kind in _PRODUCT_KINDS:
        readiness = read_port.margin_readiness(scope, variant_id=scope.entity_id)
        blockers = _cost_blockers(
            scope.entity_id, readiness.missing_components, readiness.stale_components
        )
        facts = FlowFacts(
            flow=FlowKind.INVESTIGATION.value,
            blockers=blockers,
            next_blocker_code=blockers[0].code if blockers else None,
            state_keys=[readiness.state],
            deep_link=ROUTE_PRODUCTS,
        )
        return FlowOutcome(flow=FlowKind.INVESTIGATION, facts=facts)
    # A subject this sub-scope has no authoritative entity read for. Fail closed
    # rather than silently widening to an account answer, which would lose the
    # turn's subject (§8.1: exactly one active context, never re-guessed).
    return FlowOutcome(
        flow=FlowKind.INVESTIGATION, reason=REASON_UNSUPPORTED_SUBJECT, failed=True
    )


def _simulation(
    scope: TurnScope, subject_kind: str | None, read_port: ReadPort
) -> FlowOutcome:
    """Simulation (Journey 8, CHAT-032). Non-executable BY CONSTRUCTION.

    The facts carry ``simulation=True``, no Draft, and no transition — and there
    is no control field on :class:`FlowFacts` to carry one even if a caller
    wanted to. Evidence reads are authoritative; the model relays, never computes.

    **Explicitly-planned scope boundary.** This does NOT call ``simulatePolicy``.
    That endpoint takes a FULLY-SPECIFIED what-if — ``currentPrice``, every
    contribution component, readiness and the policy config — and nothing in the
    turn can supply those deterministically today, which would leave the MODEL
    authoring money into an engine request. That is forbidden outright (§12.3:
    engine outputs for every numeric financial value; §9.1 money correctness), so
    the what-if assembly stays out of this sub-scope rather than being improvised.
    The flow fails CLOSED on an unsupported subject and its negative test pins
    that; the downstream completer is the deterministic what-if assembly (from the
    cost profile + guardrail reads), which is a separate reviewed change.
    """
    if scope.entity_id is not None and subject_kind in _RECOMMENDATION_KINDS:
        detail = read_port.recommendation_detail(scope, recommendation_id=scope.entity_id)
        facts = _detail_facts(FlowKind.SIMULATION, detail)
        facts.simulation = True
        facts.deep_link = ROUTE_MARKET
        return FlowOutcome(flow=FlowKind.SIMULATION, facts=facts)
    guardrails = read_port.guardrails(scope)
    facts = FlowFacts(
        flow=FlowKind.SIMULATION.value,
        simulation=True,
        state_keys=[str(guardrails.version)],
        deep_link=ROUTE_MARKET,
    )
    return FlowOutcome(flow=FlowKind.SIMULATION, facts=facts)


def _monitoring(scope: TurnScope, read_port: ReadPort) -> FlowOutcome:
    """Execution monitoring (Journey 10, CHAT-073/074). Read-only, never a retry.

    Actions are grouped by their authoritative §8.4 state in the order the read
    returned them. Retry guidance is a DEEP LINK only — there is no execute path
    in the model plane, so an unreconciled action cannot be retried from here.
    """
    actions = read_port.actions(scope)
    groups: dict[str, list[str]] = {}
    for row in actions.actions:
        groups.setdefault(row.state, []).append(row.action_id)
    facts = FlowFacts(
        flow=FlowKind.MONITORING.value,
        action_groups=groups,
        amounts=[row.price for row in actions.actions if row.price is not None],
        deep_link=ROUTE_ACTIONS,
    )
    return FlowOutcome(flow=FlowKind.MONITORING, facts=facts)


def _administration(scope: TurnScope, read_port: ReadPort) -> FlowOutcome:
    """Administration: Level-1 read + explanation + Settings deep link (§8.3).

    No Draft: a Level-2 reversible proposal classifies as PrepareAction (PD-4),
    and Level-3 commercial guardrails are explanation + deep link ONLY (CHAT-062)
    — there is no L3 write tool in the registry, so there is nothing to call.
    """
    guardrails = read_port.guardrails(scope)
    facts = FlowFacts(
        flow=FlowKind.ADMINISTRATION.value,
        state_keys=[str(guardrails.version)],
        deep_link=level3_explanation(),
    )
    return FlowOutcome(flow=FlowKind.ADMINISTRATION, facts=facts)


def _navigation(subject_kind: str | None) -> FlowOutcome:
    """Navigation: deterministic deep-link resolution from the CLOSED route map.

    No card, no Draft, and no read — the target is a pure function of the
    resolved chip kind. An unknown kind resolves to the Today workspace, the
    canonical "what requires attention now" surface, never a model-authored path.
    """
    route = _NAVIGATION_ROUTES.get(subject_kind or "", ROUTE_TODAY)
    return FlowOutcome(
        flow=FlowKind.NAVIGATION,
        facts=FlowFacts(flow=FlowKind.NAVIGATION.value, deep_link=route),
    )


def _prepare_action(
    scope: TurnScope,
    subject_kind: str | None,
    read_port: ReadPort,
    draft_port: DraftPort | None,
) -> FlowOutcome:
    """Prepare Action — the ONLY class with Draft capability (§8.2, #31).

    Deterministic and single-shot:

    1. read the recommendation's authoritative PRC-001 record (which also carries
       the tenant this plane re-checks and the variant the Draft targets);
    2. if the target is BLOCKED, return blocker guidance — ONE blocker at a time
       in the engine's policy order (CHAT-071) — instead of a refused Draft, and
       originate NOTHING;
    3. otherwise call :func:`~llm.flows.actions.prepare_action` exactly ONCE. The
       identifiers come from the authoritative read, never from the model.

    The write is terminal at Draft: there is no approve/execute/confirm method on
    :class:`~llm.flows.ports.DraftPort` to call next.
    """
    if scope.entity_id is None or subject_kind not in _RECOMMENDATION_KINDS:
        return FlowOutcome(
            flow=FlowKind.PREPARE_ACTION, reason=REASON_NO_SUBJECT, failed=True
        )
    detail = read_port.recommendation_detail(scope, recommendation_id=scope.entity_id)
    blockers = _policy_blockers(detail.blockers)
    if blockers or not detail.approvable:
        facts = _detail_facts(FlowKind.PREPARE_ACTION, detail)
        pending = next_blocker(
            [
                Blocker(code=b.code, reason_key=b.code, affected_count=1)
                for b in blockers
            ]
        )
        facts.next_blocker_code = pending.code if pending is not None else None
        return FlowOutcome(
            flow=FlowKind.PREPARE_ACTION, facts=facts, reason=REASON_BLOCKED_TARGET
        )
    if draft_port is None:
        # No Draft transport is wired. Fail closed — never a fabricated Draft.
        return FlowOutcome(
            flow=FlowKind.PREPARE_ACTION, reason=REASON_NO_DRAFT_PORT, failed=True
        )
    try:
        result = prepare_action(
            draft_port,
            account_id=scope.marketplace_account_id,
            entity_id=detail.variant_id,
            recommendation_id=detail.id,
        )
    except Exception:  # noqa: BLE001 - any Draft failure fails the turn closed
        return FlowOutcome(
            flow=FlowKind.PREPARE_ACTION, reason=REASON_DRAFT_UNAVAILABLE, failed=True
        )
    facts = _detail_facts(FlowKind.PREPARE_ACTION, detail)
    facts.draft = result.draft
    facts.transitions = list(result.transitions)
    facts.deep_link = ROUTE_ACTIONS
    return FlowOutcome(flow=FlowKind.PREPARE_ACTION, facts=facts)


# --- shared projections ------------------------------------------------------


def _detail_facts(flow: FlowKind, detail: RecommendationDetailRead) -> FlowFacts:
    """Project a PRC-001 record onto authoritative facts. Order is the engine's."""
    return FlowFacts(
        flow=flow.value,
        amounts=detail.money_amounts(),
        blockers=_policy_blockers(detail.blockers),
        state_keys=[detail.readiness, detail.evidence_quality],
        deep_link=ROUTE_ACTIONS,
    )


def _policy_blockers(blockers: list[PolicyBlockerRead]) -> list[FlowBlocker]:
    """Relay policy blockers in the ENGINE's precedence order (CHAT-070, §9.3).

    Sorted by the engine's own ``stageOrder`` — the numeric precedence the policy
    engine emitted — not by a second opinion held in this plane. The sort is
    stable, so equal-stage blockers keep their engine order.
    """
    ordered = sorted(blockers, key=lambda b: b.stage_order)
    return [
        FlowBlocker(code=b.code, stage=b.stage, order=b.stage_order) for b in ordered
    ]


def _cost_blockers(
    entity_id: str, missing: list[str], stale: list[str]
) -> list[FlowBlocker]:
    """Cost-readiness blockers in ``AllComponents`` order (CHAT-070, §9.2).

    Ordered by :func:`~llm.flows.blockers.order_cost_blockers`, which mirrors the
    Go ``internal/cost`` constant — a mirror, never a second opinion. Each carries
    the single-value cost-entry deep link (CHAT-071); complex diagnosis
    (CSV import) is the structured screen's job, not chat's.
    """
    codes = [*missing, *stale]
    known = [
        Blocker(code=code, reason_key=code, affected_count=1)
        for code in codes
        if code in COST_COMPONENT_ORDER
    ]
    ordered = order_cost_blockers(known)
    return [
        FlowBlocker(
            code=b.code,
            stage="cost_readiness",
            order=COST_COMPONENT_ORDER.index(b.code),
            cost_entry_deep_link=cost_entry(entity_id),
        )
        for b in ordered
    ]


# --- the merge that makes byte-match structural (hazard H3) ------------------


def apply_flow_facts(
    answer: dict[str, Any] | None, facts: dict[str, Any] | None
) -> dict[str, Any] | None:
    """Write the deterministic facts OVER the model's answer. Never the reverse.

    The model may compose free text around the facts; it may not author them. So:

    * the whole authoritative block is attached under ``"flow"``, verbatim;
    * ``amounts`` is REPLACED by the engine figures — a model-authored monetary
      value is DISCARDED, never merged, never reconciled (§12.3: every numeric
      financial value comes from an engine output).

    The model's own natural-language slots are untouched. Called on both the
    buffered and streamed paths from the ONE place each builds its envelope, so
    the two cannot drift.
    """
    if facts is None:
        return answer
    merged: dict[str, Any] = dict(answer or {})
    merged["flow"] = facts
    merged["amounts"] = list(facts.get("amounts", []))
    return merged


def _emit(outcome: FlowOutcome) -> None:
    record = {
        "metric": FLOW_DISPATCH_METRIC,
        "flow": outcome.flow.value,
        "outcome": "failed" if outcome.failed else "dispatched",
        "reason": outcome.reason,
    }
    if outcome.failed:
        _LOGGER.warning("flow_dispatch_failed", extra=record)
    else:
        _LOGGER.info("flow_dispatch", extra=record)
