"""Per-intent tool capabilities (PRD §8.2, §12.3, CHAT-003; issue #31).

The routing contract must express MORE than "may this intent use a tool?": the
shared registry holds both read and Draft-only tools, so a single boolean cannot
distinguish *read-only* authority from *Draft-only* authority. The never-cut
invariant is that ONLY *Prepare Action* may originate a Draft (§8.2); the other
tool-capable classes must not inherit Draft capability merely because they can
read.

This module is the single source of that authority: an explicit, per-class
:class:`IntentCapability` naming the tool KINDS and the exact tool NAMES each
intent is granted. The default is :data:`EMPTY_CAPABILITY` — nothing. Draft
capability is granted to :data:`~llm.intents.models.IntentClass.PREPARE_ACTION`
and to no one else. A lookup miss (a hypothetically new / unmapped intent) also
returns the EMPTY capability: the contract fails closed.

The data here is plain and JSON-safe (frozensets of enum/str) — no framework
type enters it (CLAUDE.md engineering method).
"""

from __future__ import annotations

from dataclasses import dataclass, field

from llm.intents.models import IntentClass
from llm.tools.registry import DRAFT_TOOL_NAMES, READ_TOOL_NAMES, ToolKind


@dataclass(frozen=True)
class IntentCapability:
    """What one intent class is granted against the tool registry.

    ``allowed_kinds`` bounds authority by tool KIND (read vs Draft): a tool binds
    only if its registry kind is in this set. ``tool_names`` is the explicit set
    of tool names the class may bind. Both must permit a tool for it to bind — so
    naming a Draft tool is inert unless :data:`~llm.tools.registry.ToolKind.DRAFT`
    is also granted. The default is empty: an intent is granted NOTHING unless it
    is listed here explicitly (fail closed).
    """

    allowed_kinds: frozenset[ToolKind] = field(default_factory=frozenset)
    tool_names: frozenset[str] = field(default_factory=frozenset)


# The single EMPTY capability: shared default for guidance-only intents and for
# any unmapped/new intent. Its identity is asserted by the fail-closed test.
EMPTY_CAPABILITY = IntentCapability()

# Read-only authority granted to the five read-capable intents. Draft capability
# (DRAFT kind + the draft tool names) is added ONLY for PrepareAction below.
_READ_ONLY = frozenset({ToolKind.READ})
_READ_AND_DRAFT = frozenset({ToolKind.READ, ToolKind.DRAFT})

# Explicit per-intent grants. The two guidance-only classes (ApproveAction and
# ConfirmResult) are deliberately ABSENT — they resolve to EMPTY_CAPABILITY via
# the fail-closed lookup, so they bind zero tools.
_CAPABILITIES: dict[IntentClass, IntentCapability] = {
    # Question: broad investigation / briefing — every read, no Draft.
    IntentClass.QUESTION: IntentCapability(
        allowed_kinds=_READ_ONLY,
        tool_names=READ_TOOL_NAMES,
    ),
    # Simulation: engine + evidence reads to model an outcome — no Draft.
    IntentClass.SIMULATION: IntentCapability(
        allowed_kinds=_READ_ONLY,
        tool_names=frozenset(
            {"read_catalog", "read_identity", "read_observation", "read_margin", "read_policy"}
        ),
    ),
    # Prepare Action: the ONLY class granted Draft authority. Reads first, then
    # originates a Draft (never advances past it).
    IntentClass.PREPARE_ACTION: IntentCapability(
        allowed_kinds=_READ_AND_DRAFT,
        tool_names=READ_TOOL_NAMES | DRAFT_TOOL_NAMES,
    ),
    # Review Action: reads the prepared Draft / action state and its engine
    # backing — no Draft authority (review never re-drafts here).
    IntentClass.REVIEW_ACTION: IntentCapability(
        allowed_kinds=_READ_ONLY,
        tool_names=frozenset({"read_action", "read_policy", "read_margin", "read_observation"}),
    ),
    # Administration: Level-1 reads (connection / readiness / strategy) — no
    # Draft (Level-2 proposals are a PrepareAction concern; §8.3).
    IntentClass.ADMINISTRATION: IntentCapability(
        allowed_kinds=_READ_ONLY,
        tool_names=frozenset({"read_settings", "read_catalog"}),
    ),
    # Navigation: minimal reads to resolve where to deep-link — no Draft.
    IntentClass.NAVIGATION: IntentCapability(
        allowed_kinds=_READ_ONLY,
        tool_names=frozenset({"read_catalog", "read_settings"}),
    ),
}


def capability_for(intent: IntentClass) -> IntentCapability:
    """Return the explicit capability for an intent, EMPTY on a lookup miss.

    Total and fail-closed: guidance-only and any unmapped/new intent resolve to
    :data:`EMPTY_CAPABILITY` (grants nothing), never to a permissive default.
    """
    return _CAPABILITIES.get(intent, EMPTY_CAPABILITY)
