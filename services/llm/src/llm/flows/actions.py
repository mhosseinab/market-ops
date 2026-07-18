"""Prepare / bulk / admin flows (Journeys 8–9, PRD §6.9–6.10, §8.3).

Every write here terminates at a Draft (§8.2): Prepare-Action creates a
recommendation Draft, bulk creates a versioned selection-set Draft, Level-2 admin
creates a before/after/scope/consequence proposal Draft. None advances past
Draft; none confirms, approves, or bulk-approves — those paths do not exist in
this module or on :class:`~llm.flows.ports.DraftPort`. Confirmation is always the
external structured control the returned card deep-links to.

Level-3 admin (commercial guardrails) is explanation + deep-link ONLY (CHAT-062):
:func:`level3_explanation` returns guidance with no Draft and no write — there is
no L3 write tool in the registry, so there is nothing to call.
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict

from llm.flows.deep_links import level3_explanation as _level3_deep_link
from llm.flows.models import (
    DraftTicket,
    GuidanceOnly,
    ProposalCard,
    TransitionKind,
)
from llm.flows.ports import DraftPort


class PrepareResult(BaseModel):
    """The outcome of a Prepare-Action turn: a Draft plus the transition it made."""

    model_config = ConfigDict(extra="forbid")

    draft: DraftTicket
    transitions: list[TransitionKind]


def prepare_action(
    port: DraftPort, *, account_id: str, entity_id: str, recommendation_id: str
) -> PrepareResult:
    """Prepare an individual approval Draft (CHAT-040). Terminal at Draft.

    Creates a recommendation-card Draft via the Draft-only port and records the
    ONE transition the plane can make. The approval CARD is rendered by the
    gateway from S17 state; chat displays it and the confirmation goes through the
    external structured control the Draft references — chat never owns a confirm
    path (CHAT-041/042).
    """
    draft = port.create_recommendation_draft(
        account_id=account_id, entity_id=entity_id, recommendation_id=recommendation_id
    )
    return PrepareResult(draft=draft, transitions=[TransitionKind.DRAFT])


class BulkResult(BaseModel):
    """The outcome of a bulk handoff: a versioned selection-set Draft.

    Bulk in chat is filter + counts + aggregate impact + a handoff that creates an
    EXACT versioned selection set (CHAT-050). There is NO chat bulk approval
    (CHAT-051): the selection set is a Draft the seller approves through the
    external structured control per the platform's bulk path.
    """

    model_config = ConfigDict(extra="forbid")

    selection_set: DraftTicket
    affected_count: int
    transitions: list[TransitionKind]


def prepare_selection_set(
    port: DraftPort, *, account_id: str, query: str, affected_count: int
) -> BulkResult:
    """Create a versioned bulk selection-set Draft (CHAT-050). No bulk approve."""
    draft = port.create_selection_set_draft(account_id=account_id, query=query)
    return BulkResult(
        selection_set=draft,
        affected_count=affected_count,
        transitions=[TransitionKind.DRAFT],
    )


class Level2Result(BaseModel):
    """A Level-2 reversible-config proposal card plus its Draft transition."""

    model_config = ConfigDict(extra="forbid")

    card: ProposalCard
    transitions: list[TransitionKind]


def propose_level2_change(
    port: DraftPort,
    *,
    account_id: str,
    setting_key: str,
    before_key: str,
    after_key: str,
) -> Level2Result:
    """Propose a Level-2 reversible config change (CHAT-061). Terminal at Draft.

    Produces a before/after/scope/consequence proposal card with a structured
    confirmation + audit path owned by the gateway. The confirmation control is
    external; this flow only drafts the proposal.
    """
    card = port.create_level2_proposal(
        account_id=account_id,
        setting_key=setting_key,
        before_key=before_key,
        after_key=after_key,
    )
    return Level2Result(card=card, transitions=[TransitionKind.DRAFT])


def level3_explanation(*, guidance_key: str) -> GuidanceOnly:
    """Level-3 admin: explanation + deep-link ONLY (CHAT-062). No write, no Draft.

    Commercial guardrails have no chat write tool in P0; a request to change one
    is answered with guidance and a deep link to the structured screen (sourced
    from the single deep-link map). No transition is recorded — there is nothing
    to originate.
    """
    return GuidanceOnly(guidance_key=guidance_key, deep_link=_level3_deep_link())
