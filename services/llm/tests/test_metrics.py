"""Free-text-containment observability (B4, CLAUDE.md observability pillars).

The containment boundary emits a counter and a structured log with stable,
locale-neutral field names — telemetry can distinguish a contained approval
attempt from a silent bypass. No user free text (Persian or English) is ever a
diagnostic identifier.
"""

from __future__ import annotations

import logging

import pytest
from llm.metrics import FREE_TEXT_CONTAINMENT_METRIC, ContainmentMetrics


def test_counter_increments_per_intent() -> None:
    m = ContainmentMetrics()
    m.record_containment("ApproveAction")
    m.record_containment("ApproveAction")
    m.record_containment("ConfirmResult")
    assert m.total == 3
    assert m.by_intent == {"ApproveAction": 2, "ConfirmResult": 1}


def test_structured_log_fields_are_stable_and_locale_neutral(
    caplog: pytest.LogCaptureFixture,
) -> None:
    m = ContainmentMetrics()
    with caplog.at_level(logging.INFO, logger="llm.containment"):
        m.record_containment("ApproveAction")

    record = caplog.records[-1]
    assert record.message == "free_text_containment"
    assert record.metric == FREE_TEXT_CONTAINMENT_METRIC  # type: ignore[attr-defined]
    assert record.intent == "ApproveAction"  # type: ignore[attr-defined]
    assert record.disposition == "guidance_only"  # type: ignore[attr-defined]
    assert record.transitions == 0  # type: ignore[attr-defined]
    # The machine intent token is logged, never the user's free-text message.
    assert "approve it" not in record.getMessage()
