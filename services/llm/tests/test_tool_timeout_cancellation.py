"""A timed-out tool is cancelled and contained, not abandoned (issue #25, §12.4).

The per-tool timeout must be a real containment boundary: when a tool overruns,
the middleware signals request-scoped cancellation so the operation receives it,
bounds the cleanup, and leaves no worker thread running past the grace window. An
abandoned, unkillable worker thread is never the authoritative boundary — a
timed-out read or Draft could otherwise commit a late, invisible write.
"""

from __future__ import annotations

import logging
import threading
import time

import pytest
from llm.orchestrator.agent import (
    TOOL_TIMEOUT_WORKER_METRIC,
    PerToolTimeoutMiddleware,
    ToolTimeoutError,
)
from llm.orchestrator.cancellation import current_cancel_token


class _Req:
    """Minimal stand-in for the middleware's tool-call request object."""

    def __init__(self, name: str) -> None:
        self.tool_call = {"name": name}


class _MetricHandler(logging.Handler):
    """Collect llm.orchestrator log records so a test can assert emitted metrics."""

    def __init__(self) -> None:
        super().__init__()
        self.records: list[logging.LogRecord] = []

    def emit(self, record: logging.LogRecord) -> None:
        self.records.append(record)


def _passthrough(request: object) -> str:
    return "ok"


def test_fast_tool_returns_through_the_middleware() -> None:
    mw = PerToolTimeoutMiddleware(timeout_seconds=1.0, cleanup_grace_seconds=1.0)
    assert mw.wrap_tool_call(_Req("read_catalog"), _passthrough) == "ok"


def test_cooperative_tool_observes_cancellation_and_no_worker_lingers() -> None:
    """A cooperative tool receives cancellation and the worker exits within grace.

    The tool polls the request-scoped cancel token (exactly what a real read/Draft
    transport does) and stops the moment it is cancelled. After the bounded cleanup
    the worker thread is gone and no lingering-worker incident is emitted.
    """
    observed_cancel = threading.Event()
    worker_thread: dict[str, threading.Thread] = {}

    def cooperative(request: object) -> str:
        worker_thread["t"] = threading.current_thread()
        token = current_cancel_token()
        assert token is not None  # the middleware publishes a token per call
        # Simulate a bounded network operation that honors cancellation.
        while not token.cancelled():
            time.sleep(0.01)
        observed_cancel.set()
        token.raise_if_cancelled()
        return "never"

    mw = PerToolTimeoutMiddleware(timeout_seconds=0.2, cleanup_grace_seconds=2.0)

    with pytest.raises(ToolTimeoutError):
        mw.wrap_tool_call(_Req("read_observation"), cooperative)

    # The underlying operation actually received cancellation ...
    assert observed_cancel.is_set()
    # ... and its worker thread is no longer running after the bounded cleanup.
    t = worker_thread["t"]
    t.join(timeout=1.0)
    assert not t.is_alive()


def test_uncancellable_tool_is_flagged_as_an_incident() -> None:
    """A tool that ignores cancellation past the grace emits an audited incident.

    We cannot kill a Python thread; a tool that refuses to observe cancellation is
    an incident, not a silent recovery — so the middleware still fails closed AND
    emits a traced, audited signal instead of silently abandoning the thread.
    """
    mw = PerToolTimeoutMiddleware(timeout_seconds=0.1, cleanup_grace_seconds=0.2)

    def uncancellable(request: object) -> str:
        time.sleep(3.0)  # ignores cancellation entirely
        return "never"

    handler = _MetricHandler()
    logger = logging.getLogger("llm.orchestrator")
    logger.addHandler(handler)
    started = time.monotonic()
    try:
        with pytest.raises(ToolTimeoutError):
            mw.wrap_tool_call(_Req("read_margin"), uncancellable)
    finally:
        logger.removeHandler(handler)
    elapsed = time.monotonic() - started

    # Fails closed promptly on the bound + grace, not after the tool's full 3s.
    assert elapsed < 2.0
    # The lingering worker is reported (metric + structured log), never silent.
    assert any(r.__dict__.get("metric") == TOOL_TIMEOUT_WORKER_METRIC for r in handler.records)
