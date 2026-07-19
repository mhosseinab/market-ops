"""Request-scoped cooperative cancellation for tool calls (§12.4, issue #25).

A per-tool wall-clock timeout must be able to STOP the operation it bounds, not
merely stop waiting for it. A Python worker thread cannot be killed, so an
abandoned thread is never the authoritative containment boundary: a timed-out
read or Draft could otherwise keep running and commit a late, invisible write.

The boundary is instead cooperative and request-scoped. Each tool call runs with
a :class:`CancelToken` published on a :class:`~contextvars.ContextVar`; the outbound
tool transport (the real read/Draft ports wired in S21/S23) reads
:func:`current_cancel_token` and aborts its in-flight network operation when the
token is cancelled, exactly as an ``asyncio`` task receives ``CancelledError`` on
the streamed path. On timeout the middleware cancels the token, so the deadline
propagates all the way to the network operation instead of stopping at the thread
boundary.

The token is JSON-irrelevant runtime state (a :class:`threading.Event`); it never
enters graph state, an owned contract, or ``gen/*``.
"""

from __future__ import annotations

import contextvars
import threading


class ToolCancelledError(Exception):
    """The active tool call was cancelled because its per-tool deadline elapsed.

    A cooperative tool/transport raises this (via :meth:`CancelToken.raise_if_cancelled`)
    when it observes cancellation, so the operation fails closed at its own seam
    instead of running to completion behind an abandoned worker thread.
    """


class CancelToken:
    """A one-shot, thread-safe cancellation signal for a single tool call.

    Cancellation is monotonic: once cancelled it stays cancelled. Both the sync
    worker thread and any transport it drives can poll :meth:`cancelled` or block
    on :meth:`wait` for the bounded cleanup grace.
    """

    __slots__ = ("_event",)

    def __init__(self) -> None:
        self._event = threading.Event()

    def cancel(self) -> None:
        """Signal cancellation. Idempotent."""
        self._event.set()

    def cancelled(self) -> bool:
        """True once the deadline has elapsed and the call must stop."""
        return self._event.is_set()

    def raise_if_cancelled(self) -> None:
        """Raise :class:`ToolCancelledError` if cancellation has been signalled."""
        if self._event.is_set():
            raise ToolCancelledError("tool call cancelled: per-tool deadline elapsed (§12.4)")

    def wait(self, timeout: float) -> bool:
        """Block up to ``timeout`` seconds for cancellation; return the cancelled state."""
        return self._event.wait(timeout)


_current: contextvars.ContextVar[CancelToken | None] = contextvars.ContextVar(
    "llm_tool_cancel_token", default=None
)


def current_cancel_token() -> CancelToken | None:
    """The cancellation token for the tool call running in this context, if any.

    Outbound tool transports call this and honor cancellation; when no tool call is
    in flight (or on a path that does not bound tools) it is ``None``.
    """
    return _current.get()


def set_cancel_token(token: CancelToken) -> contextvars.Token[CancelToken | None]:
    """Publish ``token`` as the active cancellation token; returns the reset handle."""
    return _current.set(token)


def reset_cancel_token(reset: contextvars.Token[CancelToken | None]) -> None:
    """Restore the previous cancellation token using the handle from :func:`set_cancel_token`."""
    _current.reset(reset)
