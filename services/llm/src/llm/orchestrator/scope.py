"""Request-scoped AUTHORITATIVE turn scope for outbound reads (issue #108, 108c).

The model-visible read tools take ``marketplace_account_id`` / ``entity_id`` as
MODEL-AUTHORED arguments (``llm.tools.registry``). While every runner was a
fail-closed stub that was inert. The moment a runner reaches the gateway, a model
that emitted a foreign account id would produce a LIVE CROSS-TENANT READ — the
exact class of hole issues #412/#419 just closed in the database. It must not be
re-opened one layer up.

This module is the seam that prevents it. The turn's authoritative scope — the
gateway-asserted ``organization_id`` / ``marketplace_account_id`` carried on
``TurnState`` (108b established they originate in the persisted conversation row,
NOT in the untrusted ``turn_context`` payload) plus the entity the deterministic
resolver settled — is published on a :class:`~contextvars.ContextVar` for the
duration of the agent step. A production read runner reads it and scopes EVERY
outbound call by it; a model-authored identifier is only ever COMPARED against
it, never used.

The mechanism deliberately mirrors :mod:`llm.orchestrator.cancellation` (the
existing request-scoped-token precedent) rather than inventing a second one:

* the registry is a process-wide singleton and the scope is per-request, so an
  ambient, context-local binding is the only correct shape;
* a :class:`~contextvars.ContextVar` is per-asyncio-Task, so two concurrent SSE
  turns never observe each other's scope;
* ``contextvars.copy_context()`` — which
  :class:`~llm.orchestrator.agent.PerToolTimeoutMiddleware` already performs for
  the sync tool-call path — carries the binding onto the worker thread.

The scope is runtime state, never graph state: it holds no framework type, never
enters an owned contract or ``gen/*``, and is never serialized.
"""

from __future__ import annotations

import contextvars
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass

__all__ = [
    "TurnScope",
    "current_turn_scope",
    "turn_scope",
]


@dataclass(frozen=True)
class TurnScope:
    """The AUTHORITATIVE tenant/subject scope of one turn. Never model-authored.

    ``organization_id`` is the caller's authenticated organization and
    ``marketplace_account_id`` is the account the gateway resolved for the turn
    from the persisted conversation row. ``entity_id`` is the subject the
    DETERMINISTIC context resolver settled (``active_context``), or ``None`` when
    the turn is account-scoped — an unresolved subject is never guessed.

    Frozen: a runner can compare against it but can never widen it.
    """

    organization_id: str
    marketplace_account_id: str
    entity_id: str | None = None


_current: contextvars.ContextVar[TurnScope | None] = contextvars.ContextVar(
    "llm_turn_scope", default=None
)


def current_turn_scope() -> TurnScope | None:
    """The authoritative scope of the turn running in this context, if any.

    ``None`` means no turn scope has been published — a production read runner
    MUST fail closed on that rather than fall back to a model-supplied
    identifier (there is deliberately no default scope to fall back to).
    """
    return _current.get()


@contextmanager
def turn_scope(scope: TurnScope | None) -> Iterator[None]:
    """Publish ``scope`` for the duration of the block, then restore the previous.

    ``None`` is published verbatim (it does NOT mean "leave the previous scope in
    place"): a turn that could not establish an authoritative scope must leave
    the ambient scope EMPTY so a read runner fails closed instead of inheriting a
    neighbouring turn's tenant.
    """
    reset = _current.set(scope)
    try:
        yield
    finally:
        _current.reset(reset)
