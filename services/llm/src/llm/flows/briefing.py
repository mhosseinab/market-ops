"""Daily briefing (Journey 7, PRD §6.8, CHAT-010/011). Deterministic.

The briefing is generated once per business day per account from the Today
ranking. Its event IDs and their order MUST equal the Today feed byte-for-byte
(CHAT-010): the same pricing-engine ranking drives the screen, the chat briefing,
and the S19 email, so a seller sees one system, not three. Canonical briefing
questions ("how many are blocked?", "how many changed?") are answered from
ground-truth counts derived from that same feed — never from a model guess
(CHAT-011).

This module is pure: the River job (Go) fetches the feed and persists the
briefing; the chat plane renders and answers questions from the SAME feed shape.
"""

from __future__ import annotations

from collections import Counter

from pydantic import BaseModel, ConfigDict, Field


class TodayRankItem(BaseModel):
    """One ranked item in the Today feed (the pricing engine's output)."""

    model_config = ConfigDict(extra="forbid")

    event_id: str
    entity_id: str
    rank: int
    headline_key: str  # catalog key for the row headline (localization boundary)
    state_key: str  # canonical state/glossary key (CHAT-022)


class TodayFeed(BaseModel):
    """The account's ranked Today feed for a business day.

    ``items`` arrive already ranked by the pricing engine; the briefing preserves
    that exact order. The feed is the single source both the screen and the chat
    briefing read (CHAT-010).
    """

    model_config = ConfigDict(extra="forbid")

    account_id: str
    business_day: str  # YYYY-MM-DD (Jalali is a display calendar over UTC storage)
    items: list[TodayRankItem] = Field(default_factory=list)

    def event_ids(self) -> list[str]:
        """The event IDs in ranked order — the CHAT-010 equality target."""
        return [item.event_id for item in self.items]


class Briefing(BaseModel):
    """A generated daily briefing: the ordered feed plus ground-truth counts.

    ``items`` is a *copy* of the feed's ranked items — same IDs, same order. The
    briefing never re-ranks, filters, or drops an item; if the screen shows it,
    the briefing shows it, in the same place (CHAT-010).
    """

    model_config = ConfigDict(extra="forbid")

    account_id: str
    business_day: str
    items: list[TodayRankItem] = Field(default_factory=list)
    counts_by_state: dict[str, int] = Field(default_factory=dict)

    def event_ids(self) -> list[str]:
        return [item.event_id for item in self.items]


def build_briefing(feed: TodayFeed) -> Briefing:
    """Generate the briefing for a business day from the Today feed. Pure.

    Order-preserving by construction: ``items`` is the feed's items in the same
    sequence, so ``briefing.event_ids() == feed.event_ids()`` always holds
    (CHAT-010). Counts are derived from the feed, not authored (CHAT-011).
    """
    counts = Counter(item.state_key for item in feed.items)
    return Briefing(
        account_id=feed.account_id,
        business_day=feed.business_day,
        items=list(feed.items),
        counts_by_state=dict(counts),
    )


# Canonical briefing questions (CHAT-011): each maps to a ground-truth count over
# the feed. The chat plane answers from these deterministic reducers, never from a
# model estimate. Keys are stable machine tokens; the surface localizes them.
def count_total(briefing: Briefing) -> int:
    """How many ranked items the briefing covers (== feed length)."""
    return len(briefing.items)


def count_in_state(briefing: Briefing, state_key: str) -> int:
    """How many items are in a given canonical state (CHAT-011 ground truth)."""
    return briefing.counts_by_state.get(state_key, 0)
