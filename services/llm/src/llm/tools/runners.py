"""Production runners for the model-visible READ tools (issue #108, 108c).

These are the bodies wired behind the registry's READ tools by the production
seam (``build_registry(production_read_runners=…)``). They turn a model tool call
into an authoritative gateway read through :class:`~llm.flows.read_ports.ReadPort`.

**The hazard they exist to close (H1).** The registry's read tools declare
``marketplace_account_id`` and ``entity_id`` as MODEL-AUTHORED arguments. While
every runner was a fail-closed stub that was inert; the moment a runner reaches
the gateway, a model that emitted a foreign account id would produce a live
CROSS-TENANT READ — the class of hole #412/#419 just closed at the database.

So these runners NEVER use a model-authored identifier as authority:

* every outbound call is scoped by the turn's AUTHORITATIVE
  :class:`~llm.orchestrator.scope.TurnScope` (the gateway-asserted organization /
  account, and the entity the DETERMINISTIC resolver settled) — never by the
  tool argument and never by the untrusted ``turn_context`` payload;
* a model-authored identifier that DIFFERS from that scope fails the call closed
  and EMITS. It is never silently accepted, and it is never silently
  coerced-and-forgotten — a coercion without an emitted event is also a bug;
* when no authoritative scope is published (nothing to scope by), the call fails
  closed. There is deliberately no fallback to the tool argument.

**The result is DATA, never an instruction (§12.1).** A failure is returned as a
structured, machine-token payload — the same fail-closed shape the S20 stub uses
— rather than raised, so a read the model cannot have is answered with "not
available" instead of aborting the turn. The model then has no number to state,
and the response contract requires it to say so rather than guess.

**No money is parsed here.** Amounts arrive already exact
(:class:`~llm.envelope.models.Money`) from the read port and are re-serialized in
their signed-decimal STRING wire form (``mode="json"``). There is no ``float`` on
this path (§9.1, never-cut).
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from typing import Any

from llm.flows.read_ports import ReadPort, ReadUnavailable
from llm.orchestrator.scope import TurnScope, current_turn_scope

__all__ = ["READ_SCOPE_METRIC", "build_production_read_runners"]

# Stable, locale-neutral telemetry identifier for the tenant/subject containment
# boundary. A model that tried to read outside the turn's authoritative scope is
# an observable event, never a silent coercion (CLAUDE.md observability). No
# tenant identifier, no entity id and no free text is recorded — only the tool
# name and a machine reason token.
READ_SCOPE_METRIC = "llm_read_scope_violation_total"
_LOGGER = logging.getLogger("llm.tools.runners")

# Machine reason tokens (never user-facing copy, never localized).
_NO_SCOPE = "no_authoritative_scope"
_ACCOUNT_MISMATCH = "account_scope_mismatch"
_ENTITY_MISMATCH = "entity_scope_mismatch"
_NO_ENTITY = "no_resolved_entity"
_READ_UNAVAILABLE = "read_unavailable"


def _unavailable(tool: str, reason: str) -> dict[str, Any]:
    """The fail-closed structured DATA payload for a read that cannot be served."""
    return {
        "status": "unavailable",
        "tool": tool,
        "kind": "read",
        "reason": reason,
    }


def _record_violation(tool: str, reason: str) -> None:
    _LOGGER.warning(
        "read_scope_violation",
        extra={
            "metric": READ_SCOPE_METRIC,
            "tool": tool,
            "reason": reason,
            "disposition": "fail_closed",
        },
    )


def _authoritative_scope(tool: str, kwargs: dict[str, Any]) -> TurnScope | str:
    """Resolve the turn's scope and reject a model-authored account that differs.

    Returns the :class:`TurnScope` on success, or a machine reason token on
    refusal. The model-authored ``marketplace_account_id`` is used ONLY as
    something to compare against; it never becomes the scope.
    """
    scope = current_turn_scope()
    if scope is None:
        _record_violation(tool, _NO_SCOPE)
        return _NO_SCOPE
    claimed = kwargs.get("marketplace_account_id")
    if isinstance(claimed, str) and claimed != scope.marketplace_account_id:
        _record_violation(tool, _ACCOUNT_MISMATCH)
        return _ACCOUNT_MISMATCH
    return scope


def _authoritative_entity(tool: str, scope: TurnScope, kwargs: dict[str, Any]) -> str | None:
    """The entity the DETERMINISTIC resolver settled, or ``None`` when refused.

    A model-authored ``entity_id`` that differs from the resolved subject is a
    refusal, not a pivot: the turn has exactly one active context (§8.1) and the
    model may not change it mid-turn. A turn with no resolved subject cannot serve
    an entity-scoped read at all.
    """
    if scope.entity_id is None:
        _record_violation(tool, _NO_ENTITY)
        return None
    claimed = kwargs.get("entity_id")
    if isinstance(claimed, str) and claimed != scope.entity_id:
        _record_violation(tool, _ENTITY_MISMATCH)
        return None
    return scope.entity_id


def build_production_read_runners(
    port: ReadPort, *, business_day: str
) -> dict[str, Callable[..., dict[str, Any]]]:
    """Build the production runner map for the READ tools this seam can serve.

    Only the read tools with a real endpoint in the frozen owned contract are
    wired. ``read_catalog`` / ``read_identity`` / ``read_observation`` are
    deliberately NOT wired here and keep their explicitly-planned fail-closed S20
    stub: binding them needs an entity-search / identity read this plane may not
    add to ``contracts/gateway.openapi.yaml`` (held by lane #87). Their stub
    returns ``status: unavailable``, so they degrade observably rather than
    fabricating a read.

    ``business_day`` is the turn's as-of business day, stamped deterministically
    from the server clock at the transport — never asserted by a caller and never
    chosen by the model (§12.3: a back-dated window must not read as current).
    """

    def read_action(**kwargs: Any) -> dict[str, Any]:
        scope = _authoritative_scope("read_action", kwargs)
        if isinstance(scope, str):
            return _unavailable("read_action", scope)
        try:
            return {"status": "ok", "actions": port.actions(scope).model_dump(mode="json")}
        except ReadUnavailable:
            return _unavailable("read_action", _READ_UNAVAILABLE)

    def read_settings(**kwargs: Any) -> dict[str, Any]:
        scope = _authoritative_scope("read_settings", kwargs)
        if isinstance(scope, str):
            return _unavailable("read_settings", scope)
        try:
            return {
                "status": "ok",
                "guardrails": port.guardrails(scope).model_dump(mode="json"),
            }
        except ReadUnavailable:
            return _unavailable("read_settings", _READ_UNAVAILABLE)

    def read_event(**kwargs: Any) -> dict[str, Any]:
        scope = _authoritative_scope("read_event", kwargs)
        if isinstance(scope, str):
            return _unavailable("read_event", scope)
        try:
            return {
                "status": "ok",
                "briefing": port.briefing(scope, business_day=business_day).model_dump(
                    mode="json"
                ),
            }
        except ReadUnavailable:
            return _unavailable("read_event", _READ_UNAVAILABLE)

    def read_margin(**kwargs: Any) -> dict[str, Any]:
        scope = _authoritative_scope("read_margin", kwargs)
        if isinstance(scope, str):
            return _unavailable("read_margin", scope)
        entity_id = _authoritative_entity("read_margin", scope, kwargs)
        if entity_id is None:
            return _unavailable("read_margin", _ENTITY_MISMATCH)
        try:
            readiness = port.margin_readiness(scope, variant_id=entity_id)
        except ReadUnavailable:
            return _unavailable("read_margin", _READ_UNAVAILABLE)
        return {"status": "ok", "readiness": readiness.model_dump(mode="json")}

    def read_policy(**kwargs: Any) -> dict[str, Any]:
        scope = _authoritative_scope("read_policy", kwargs)
        if isinstance(scope, str):
            return _unavailable("read_policy", scope)
        entity_id = _authoritative_entity("read_policy", scope, kwargs)
        if entity_id is None:
            return _unavailable("read_policy", _ENTITY_MISMATCH)
        try:
            detail = port.recommendation_detail(scope, recommendation_id=entity_id)
        except ReadUnavailable:
            return _unavailable("read_policy", _READ_UNAVAILABLE)
        # ``mode="json"`` keeps every Money mantissa in its signed-decimal STRING
        # wire form (#73 / §15.1): no int is handed on to be re-serialized lossily
        # and no float is ever produced.
        return {"status": "ok", "recommendation": detail.model_dump(mode="json")}

    return {
        "read_action": read_action,
        "read_settings": read_settings,
        "read_event": read_event,
        "read_margin": read_margin,
        "read_policy": read_policy,
    }
