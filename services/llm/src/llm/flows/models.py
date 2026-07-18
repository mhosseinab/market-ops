"""Shared, JSON-safe types for the P0 chat flows (PRD §8.2, §8.5).

These carry the ONE authority the model plane may originate — a Draft — and the
*display* references (deep links) chat uses to point at the structured controls
that live OUTSIDE the model plane. A card that can approve is rendered by the
gateway from S17 state; chat only DISPLAYS it and deep-links to the same
structured-control endpoint the screens use. Nothing here can approve, execute,
or confirm: those verbs have no representation in this module on purpose.
"""

from __future__ import annotations

from enum import StrEnum

from pydantic import BaseModel, ConfigDict, Field


class TransitionKind(StrEnum):
    """The ONLY state transition the model plane can originate: a Draft (§8.2).

    There is intentionally no ``APPROVED`` / ``EXECUTED`` / ``CONFIRMED`` member.
    A flow that wanted to emit one would have nothing to emit — the structural
    prohibition (§12.3) is the absence of the capability, and the adversarial
    suite asserts the transition ledger never contains anything but this.
    """

    DRAFT = "draft_created"


class DraftKind(StrEnum):
    """Which of the three Draft-only tools produced a Draft."""

    RECOMMENDATION = "recommendation"
    SELECTION_SET = "selection_set"
    LEVEL2_PROPOSAL = "level2_proposal"


class DraftTicket(BaseModel):
    """A reference to an S17 Draft the model plane created (never executed).

    Binds the resolved entity, account, context version, and recommendation
    version at creation (PRD §8.1) so a restored conversation re-fetches the card
    rather than reusing a cached control. ``control_deep_link`` points at the
    SAME structured-control endpoint the screens use — chat displays a card built
    from this reference and never owns the confirm path (CHAT-041/042).
    """

    model_config = ConfigDict(extra="forbid")

    draft_kind: DraftKind
    draft_id: str
    action_id: str
    account_id: str
    entity_id: str | None = None
    context_version: str
    recommendation_version: str | None = None
    parameter_version: str
    expires_at: str
    # Deep link to the external structured control (NOT the control itself).
    control_deep_link: str


class ProposalCard(BaseModel):
    """A Level-2 reversible-config proposal: before / after / scope / consequence.

    Rendered by chat as a card with a structured confirmation that goes through
    the gateway audit path (§8.3). The card is a Draft-shaped display object; the
    confirmation control is external, referenced by ``control_deep_link``.
    """

    model_config = ConfigDict(extra="forbid")

    setting_key: str
    before_key: str  # canonical catalog key for the current value's label
    after_key: str  # canonical catalog key for the proposed value's label
    scope_key: str  # what the change affects (catalog key)
    consequence_key: str  # the reversible consequence (catalog key)
    draft: DraftTicket


class CostControl(BaseModel):
    """A single-value cost-entry control descriptor for a blocker (CHAT-071).

    A structured control for entering ONE cost value via the S12 cost path — not
    a free-text field and not an approval. ``field_key`` names the cost field;
    ``deep_link`` points at the structured screen when the entry is complex
    (CSV import / multi-field diagnosis).
    """

    model_config = ConfigDict(extra="forbid")

    field_key: str
    unit_hint_key: str | None = None
    deep_link: str | None = None
    inline: bool = True  # True ⇒ single-value inline control; False ⇒ deep-link only


class GuidanceOnly(BaseModel):
    """The guidance-only outcome for Approve/Confirm intents (CHAT-041, §12.3).

    Free text never approves: an affirmative/imperative message is answered with
    a pointer to the structured control, and NO transition is recorded. Carries a
    catalog key (localization boundary) and the deep link, never an authority.
    """

    model_config = ConfigDict(extra="forbid")

    guidance_key: str
    deep_link: str
    # Always empty — a guidance-only turn never originates any transition.
    transitions: list[TransitionKind] = Field(default_factory=list)
