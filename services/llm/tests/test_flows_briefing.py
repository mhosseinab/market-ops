"""Daily briefing determinism (CHAT-010/011).

CHAT-010: the briefing's event IDs and their order MUST equal the Today feed
byte-for-byte. CHAT-011: canonical briefing questions are answered from
ground-truth counts over the same feed, never a model guess.
"""

from __future__ import annotations

from llm.flows.briefing import (
    Briefing,
    TodayFeed,
    TodayRankItem,
    build_briefing,
    count_in_state,
    count_total,
)


def _feed() -> TodayFeed:
    # An intentionally non-sorted-by-id ranking: the pricing engine's order is
    # authoritative and the briefing must preserve it exactly, not re-sort.
    def item(eid: str, pid: str, rank: int, hk: str, sk: str) -> TodayRankItem:
        return TodayRankItem(
            event_id=eid, entity_id=pid, rank=rank, headline_key=hk, state_key=sk
        )

    items = [
        item("evt-9", "p-9", 1, "h.a", "state.blocked"),
        item("evt-2", "p-2", 2, "h.b", "state.conflicted"),
        item("evt-7", "p-7", 3, "h.c", "state.blocked"),
        item("evt-1", "p-1", 4, "h.d", "state.verified"),
    ]
    return TodayFeed(account_id="acc-1", business_day="2026-07-17", items=items)


def test_briefing_event_ids_and_order_equal_today_feed() -> None:
    feed = _feed()
    briefing = build_briefing(feed)
    # CHAT-010: identical IDs, identical order — the equality gate.
    assert briefing.event_ids() == feed.event_ids()
    assert briefing.event_ids() == ["evt-9", "evt-2", "evt-7", "evt-1"]


def test_briefing_never_reorders_or_drops_items() -> None:
    feed = _feed()
    briefing = build_briefing(feed)
    assert [i.rank for i in briefing.items] == [1, 2, 3, 4]
    assert len(briefing.items) == len(feed.items)


def test_briefing_counts_are_ground_truth(  ) -> None:
    feed = _feed()
    briefing = build_briefing(feed)
    # CHAT-011: counts derived from the feed, not authored.
    assert count_total(briefing) == 4
    assert count_in_state(briefing, "state.blocked") == 2
    assert count_in_state(briefing, "state.conflicted") == 1
    assert count_in_state(briefing, "state.verified") == 1
    assert count_in_state(briefing, "state.stale") == 0


def test_empty_feed_yields_empty_briefing() -> None:
    feed = TodayFeed(account_id="acc-1", business_day="2026-07-17", items=[])
    briefing = build_briefing(feed)
    assert isinstance(briefing, Briefing)
    assert briefing.event_ids() == []
    assert count_total(briefing) == 0
