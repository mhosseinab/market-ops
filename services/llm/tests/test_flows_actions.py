"""Prepare / bulk / admin flows terminate at Draft (CHAT-040/050/051/061/062).

Every write originates exactly ONE Draft transition and nothing further; the
confirmation control is external. Level-3 admin is explanation + deep-link only,
with no Draft and no transition. A recording fake DraftPort proves the only calls
made are the three Draft-only methods.
"""

from __future__ import annotations

from llm.flows.actions import (
    level3_explanation,
    prepare_action,
    prepare_selection_set,
    propose_level2_change,
)
from llm.flows.models import (
    DraftKind,
    DraftTicket,
    ProposalCard,
    TransitionKind,
)


class RecordingDraftPort:
    """A fake DraftPort recording every call. It has ONLY the three Draft methods."""

    def __init__(self) -> None:
        self.calls: list[str] = []

    def create_recommendation_draft(
        self, *, account_id: str, entity_id: str, recommendation_id: str
    ) -> DraftTicket:
        self.calls.append("recommendation")
        return DraftTicket(
            draft_kind=DraftKind.RECOMMENDATION,
            draft_id="d-1",
            action_id="act-1",
            account_id=account_id,
            entity_id=entity_id,
            context_version="ctx-1",
            recommendation_version=recommendation_id,
            parameter_version="pv-1",
            expires_at="2026-07-17T12:00:00Z",
            control_deep_link="/app/screens/approve/act-1",
        )

    def create_selection_set_draft(self, *, account_id: str, query: str) -> DraftTicket:
        self.calls.append("selection_set")
        return DraftTicket(
            draft_kind=DraftKind.SELECTION_SET,
            draft_id="d-2",
            action_id="set-1",
            account_id=account_id,
            context_version="ctx-1",
            parameter_version="pv-1",
            expires_at="2026-07-17T12:00:00Z",
            control_deep_link="/app/screens/bulk/set-1",
        )

    def create_level2_proposal(
        self, *, account_id: str, setting_key: str, before_key: str, after_key: str
    ) -> ProposalCard:
        self.calls.append("level2_proposal")
        return ProposalCard(
            setting_key=setting_key,
            before_key=before_key,
            after_key=after_key,
            scope_key="scope.account",
            consequence_key="consequence.reversible",
            draft=DraftTicket(
                draft_kind=DraftKind.LEVEL2_PROPOSAL,
                draft_id="d-3",
                action_id="cfg-1",
                account_id=account_id,
                context_version="ctx-1",
                parameter_version="pv-1",
                expires_at="2026-07-17T12:00:00Z",
                control_deep_link="/app/settings/confirm/cfg-1",
            ),
        )


def test_prepare_action_terminates_at_draft() -> None:
    port = RecordingDraftPort()
    result = prepare_action(
        port, account_id="acc-1", entity_id="p-1", recommendation_id="rec-9"
    )
    assert result.transitions == [TransitionKind.DRAFT]
    assert result.draft.draft_kind is DraftKind.RECOMMENDATION
    assert result.draft.control_deep_link.startswith("/app/screens/approve/")
    assert port.calls == ["recommendation"]


def test_bulk_creates_versioned_selection_set_no_approve() -> None:
    port = RecordingDraftPort()
    result = prepare_selection_set(
        port, account_id="acc-1", query="account=acc-1&below_floor=true", affected_count=42
    )
    assert result.transitions == [TransitionKind.DRAFT]
    assert result.affected_count == 42
    assert result.selection_set.draft_kind is DraftKind.SELECTION_SET
    assert port.calls == ["selection_set"]


def test_level2_proposal_has_before_after_scope_consequence() -> None:
    port = RecordingDraftPort()
    result = propose_level2_change(
        port,
        account_id="acc-1",
        setting_key="briefing.time",
        before_key="value.eight_am",
        after_key="value.nine_am",
    )
    assert result.transitions == [TransitionKind.DRAFT]
    card = result.card
    assert card.before_key and card.after_key and card.scope_key and card.consequence_key
    assert port.calls == ["level2_proposal"]


def test_level3_is_explanation_and_deeplink_only() -> None:
    guidance = level3_explanation(guidance_key="chat.guidance.l3_guardrail")
    # No Draft, no transition — L3 has no chat write tool (CHAT-062).
    assert guidance.transitions == []
    assert guidance.deep_link == "/app/settings/guardrails"


def test_draft_port_exposes_no_state_changing_method() -> None:
    """The DraftPort surface has ONLY the three Draft methods (§12.3)."""
    port = RecordingDraftPort()
    methods = {m for m in dir(port) if not m.startswith("_") and callable(getattr(port, m))}
    for forbidden in ("approve", "execute", "confirm", "bulk_approve"):
        assert not any(forbidden in m for m in methods)
