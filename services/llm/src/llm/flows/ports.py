"""The Draft-only write port (PRD §8.2, §12.3). The plane's whole write surface.

A flow that needs to originate a write depends on :class:`DraftPort`, whose three
methods map 1:1 onto the three Draft-only registry tools (recommendation,
selection-set, Level-2 proposal). There is deliberately NO approve / execute /
confirm / bulk-approve / guardrail-write / permission method on this protocol —
the model plane cannot call what does not exist. The orchestrator injects a real
implementation backed by the read/Draft-only gateway credential; tests inject a
recording fake. This is the dependency-inversion seam: flows never import a
gateway client directly.
"""

from __future__ import annotations

from typing import Protocol

from llm.flows.models import DraftKind, DraftTicket, ProposalCard


class DraftPort(Protocol):
    """The only writes the model plane may originate — each terminal at Draft."""

    def create_recommendation_draft(
        self, *, account_id: str, entity_id: str, recommendation_id: str
    ) -> DraftTicket:
        """Prepare-Action: a recommendation-card Draft (CHAT-040). Never executed."""
        ...

    def create_selection_set_draft(
        self, *, account_id: str, query: str
    ) -> DraftTicket:
        """Bulk: a named, versioned selection-set Draft (CHAT-050). No bulk approve."""
        ...

    def create_level2_proposal(
        self, *, account_id: str, setting_key: str, before_key: str, after_key: str
    ) -> ProposalCard:
        """Admin L2: a reversible before/after/scope/consequence proposal (CHAT-061)."""
        ...


__all__ = ["DraftPort", "DraftKind", "DraftTicket", "ProposalCard"]
