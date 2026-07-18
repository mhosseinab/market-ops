"""Investigation filter equivalence (CHAT-033).

A conversational filter and the equivalent screen filter compile to a BYTE-EQUAL
query string, because both go through the single canonical serializer
(:func:`compile_query`). The serializer is order-insensitive and stable.
"""

from __future__ import annotations

import json
from pathlib import Path

from llm.flows.investigation import (
    FilterSpec,
    SortDir,
    SortKey,
    compile_query,
)

# The cross-boundary query golden — the canonical query bytes the SCREENS emit.
# Chat's serializer must reproduce them exactly (CHAT-033); this externalizes the
# expected bytes into a shared contract instead of proving Python self-consistency.
_GOLDEN = json.loads(
    (Path(__file__).resolve().parents[3] / "contracts" / "fixtures" / "investigation_query.json")
    .read_text(encoding="utf-8")
)


def test_compile_query_matches_cross_boundary_screen_golden() -> None:
    """chat compile_query(spec) == the screen contract's query bytes (CHAT-033)."""
    for case in _GOLDEN["cases"]:
        spec = FilterSpec(**case["spec"])
        assert compile_query(spec) == case["query"], f"query drift on {case['name']}"


def test_chat_and_screen_filters_serialize_byte_equal() -> None:
    """Same semantic filter, built two ways ⇒ identical bytes (CHAT-033)."""
    # The chat plane parses "blocked & conflicted products below floor, by exposure".
    chat_spec = FilterSpec(
        account_id="acc-1",
        states=["state.blocked", "state.conflicted"],
        below_floor=True,
        sort_key=SortKey.EXPOSURE,
        sort_dir=SortDir.DESC,
    )
    # The screen builds the same filter from its controls, in a different order.
    screen_spec = FilterSpec(
        account_id="acc-1",
        sort_dir=SortDir.DESC,
        sort_key=SortKey.EXPOSURE,
        below_floor=True,
        states=["state.conflicted", "state.blocked"],  # reversed list order
    )
    assert compile_query(chat_spec) == compile_query(screen_spec)


def test_query_is_canonical_and_stable() -> None:
    spec = FilterSpec(
        account_id="acc-1",
        states=["state.blocked"],
        below_floor=False,
        sort_key=SortKey.RANK,
    )
    q = compile_query(spec)
    # Deterministic: same input ⇒ same output every time.
    assert q == compile_query(spec)
    # Keys are sorted; account always present; booleans render true/false.
    assert q.startswith("account=acc-1")
    assert "below_floor=false" in q
    assert "sort=rank:desc" in q


def test_list_value_order_does_not_change_bytes() -> None:
    a = FilterSpec(account_id="acc-1", freshness=["freshness.stale", "freshness.aging"])
    b = FilterSpec(account_id="acc-1", freshness=["freshness.aging", "freshness.stale"])
    assert compile_query(a) == compile_query(b)


def test_unset_optionals_are_omitted() -> None:
    spec = FilterSpec(account_id="acc-1")
    q = compile_query(spec)
    assert "below_floor" not in q
    assert "sort=" not in q
    assert "state=" not in q
