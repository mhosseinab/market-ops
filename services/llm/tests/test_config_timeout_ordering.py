"""The Draft transport deadline must fire before the per-tool middleware (#25).

Fail-closed config ordering: the Draft transport timeout must be STRICTLY LESS
than the per-tool wall-clock timeout, so the network operation aborts at its own
deadline (httpx closes the connection, no ticket) rather than the middleware
firing first while the POST runs to its own, later deadline on a worker thread.
"""

from __future__ import annotations

import pytest
from llm.config import load_settings


def test_default_config_orders_draft_deadline_before_per_tool_timeout() -> None:
    settings = load_settings()
    assert settings.draft_timeout_seconds < settings.per_tool_timeout_seconds


def test_config_rejects_draft_timeout_not_strictly_less_than_per_tool() -> None:
    """draft_timeout >= per_tool_timeout is rejected at load time (fail closed)."""
    with pytest.raises(ValueError):
        load_settings(draft_timeout_seconds=15.0, per_tool_timeout_seconds=15.0)


def test_config_rejects_draft_timeout_greater_than_per_tool() -> None:
    with pytest.raises(ValueError):
        load_settings(draft_timeout_seconds=20.0, per_tool_timeout_seconds=15.0)
