"""Deterministic context resolution on the LIVE turn (PRD §8.1, CHAT-007).

This is the production consumer of :mod:`llm.contextres`: the turn-graph node
that runs AFTER free-text containment and BEFORE the agent, turns the gateway's
typed :class:`~llm.contextres.turn.TurnContext` into an authoritative
:class:`~llm.contextres.models.ResolveRequest`, and acts on the pure resolver's
outcome:

* ``RESOLVED`` — the single active chip is placed on graph state, grounding
  everything downstream (108c's deterministic flow dispatcher reads it);
* ``PICKER`` — the turn TERMINATES in the canonical structured picker card. No
  agent, no tokens, no action card, no Draft (CHAT-007: an ambiguous request
  never creates a card directly);
* ``NOT_FOUND`` — including every scope-mismatch / missing-provenance reason —
  the turn fails closed to the §12.4 structured failure + deep link. A subject is
  never fabricated.

**The tenant rule (PRD §12, §4.6 identity quarantine).** The turn's
:class:`~llm.contextres.models.RequestScope` is built ONLY from the request's own
identity fields — never from the payload. The organization/account carried INSIDE
the context payload are untrusted data validated against that scope; they are
never the scope, and a provenance-less or foreign-tenant chip fails closed.

What the two scope fields actually guarantee differs, and the check is only as
strong as the weaker one: ``organization_id`` is the caller's authenticated
organization (gateway bearer, issue #167), while ``account_id`` is the account
the GATEWAY RESOLVED for the turn from the stored conversation. A pre-existing,
out-of-scope gap (recorded on :class:`llm.app.ChatRequest`, #108 G3) means a new
conversation can name an account owned by another organization, in which case
scope and payload provenance trace to the SAME unvalidated row. Passing this
scope check is therefore necessary but NOT sufficient tenant authorization for an
authoritative read — 108c's consumers must not treat it as such.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import UTC, datetime
from typing import Any

from pydantic import ValidationError

from llm.contextres.models import (
    PickerOption,
    RequestScope,
    Resolution,
    ResolutionKind,
    ResolveRequest,
)
from llm.contextres.ports import CandidateLookupError, CandidatePort
from llm.contextres.resolver import resolve
from llm.contextres.turn import TurnContext, context_kind_for
from llm.envelope.contract import PickerCard, PickerCardOption
from llm.envelope.models import TurnFailure, screens_failure
from llm.intents.models import IntentClass
from llm.metrics import ContextResolutionMetrics

__all__ = ["ContextOutcome", "resolve_turn_context"]

# Stable outcome tokens for telemetry. ``skipped`` is the fourth, node-level
# outcome: the turn carried no context payload at all, so there was nothing to
# resolve and nothing was invented.
OUTCOME_SKIPPED = "skipped"
REASON_NO_TURN_CONTEXT = "no_turn_context"
REASON_SCOPE_MISSING = "request_scope_missing"
REASON_MALFORMED = "turn_context_malformed"
REASON_LOOKUP_FAILED = "candidate_lookup_failed"
REASON_EMPTY_PICKER = "picker_without_options"


@dataclass
class ContextOutcome:
    """What the context node decided for one turn.

    Exactly one of: ``failure`` (fail closed), ``answer`` (terminal picker) or
    ``proceed`` (the agent/flow may run). ``updates`` are the JSON-safe graph
    state keys this node writes in every case.
    """

    updates: dict[str, Any] = field(default_factory=dict)
    answer: dict[str, Any] | None = None
    failure: TurnFailure | None = None
    proceed: bool = False


def _resolution_state(
    kind: str, reason: str, resolution: Resolution | None = None
) -> dict[str, Any]:
    """The JSON-safe resolution record placed on graph state (no model objects)."""
    record: dict[str, Any] = {"kind": kind, "reason": reason}
    if resolution is not None and resolution.time_range is not None:
        record["time_range"] = resolution.time_range.model_dump(mode="json")
    return record


def picker_card(options: list[PickerOption]) -> PickerCard:
    """Project resolver picker options onto the canonical ``{id,label,contextKind}`` card.

    ``label`` is authoritative read data (or the verbatim reference token the
    caller supplied) — never model-authored copy; the surrounding copy is catalog
    keys. The card is non-executable and carries no approval control.
    """
    return PickerCard(
        options=[
            PickerCardOption(
                id=option.entity_id,
                label=option.label,
                context_kind=context_kind_for(option.context_type).value,
            )
            for option in options
        ]
    )


def resolve_turn_context(
    *,
    turn_context: dict[str, Any] | None,
    organization_id: str | None,
    account_id: str | None,
    intent: IntentClass,
    candidate_port: CandidatePort,
    metrics: ContextResolutionMetrics,
) -> ContextOutcome:
    """Resolve one turn's context deterministically. Never guesses a subject.

    ``organization_id`` / ``account_id`` are the turn's scope (see the module
    docstring for exactly what each one guarantees); the ``turn_context`` payload
    is untrusted data checked against it, never a source of it.
    """
    if turn_context is None:
        # Nothing was supplied, so there is nothing to resolve — and nothing is
        # invented. The turn proceeds with NO bound chip; a downstream flow that
        # requires one must fail closed on its absence (108c, issue #108).
        metrics.record_resolution(OUTCOME_SKIPPED, REASON_NO_TURN_CONTEXT)
        return ContextOutcome(
            updates={
                "context_resolution": {
                    "kind": OUTCOME_SKIPPED,
                    "reason": REASON_NO_TURN_CONTEXT,
                },
                "active_context": None,
            },
            proceed=True,
        )

    if not organization_id or not account_id:
        # A context payload without an authenticated tenant to validate it
        # against can NEVER resolve: the scope is not recoverable from the
        # payload (PRD §12, §4.6). Fail closed rather than trust client data.
        return _fail(
            metrics,
            code="CONTEXT_SCOPE_MISSING",
            message="the turn carries no authenticated account scope; use the structured screen",
            reason=REASON_SCOPE_MISSING,
        )

    try:
        context = TurnContext.model_validate(turn_context)
    except ValidationError:
        # A malformed/unknown-key context is a lost subject, not a droppable
        # field — fail closed rather than run against the wrong entity.
        return _fail(
            metrics,
            code="CONTEXT_MALFORMED",
            message="the turn context could not be read; use the structured screen",
            reason=REASON_MALFORMED,
        )

    scope = RequestScope(organization_id=organization_id, account_id=account_id)
    try:
        candidates = candidate_port.candidates_for(scope=scope, references=context.references)
    except CandidateLookupError:
        return _fail(
            metrics,
            code="CONTEXT_UNAVAILABLE",
            message="the assistant could not look up the referenced items; "
            "use the structured screen",
            reason=REASON_LOOKUP_FAILED,
        )

    resolution = resolve(
        ResolveRequest(
            intent=intent,
            scope=scope,
            active_context=context.to_chip(),
            references=context.references,
            candidates=candidates,
            time_phrase=context.time_phrase,
            now=context.now or _utc_now(),
            business_timezone=context.business_timezone,
            week_starts_on=context.week_starts_on,
        )
    )

    if resolution.kind is ResolutionKind.RESOLVED:
        metrics.record_resolution(resolution.kind.value, resolution.reason)
        chip = resolution.chip
        return ContextOutcome(
            updates={
                "context_resolution": _resolution_state(
                    resolution.kind.value, resolution.reason, resolution
                ),
                "active_context": chip.model_dump(mode="json") if chip is not None else None,
            },
            proceed=True,
        )

    if resolution.kind is ResolutionKind.PICKER:
        if not resolution.options:
            # An option-less picker would be a dead end inviting a guess. The
            # authoritative candidate supply that would populate it is the 108c
            # gateway read seam (issue #108); until then this fails closed.
            return _fail(
                metrics,
                code="CONTEXT_PICKER_UNAVAILABLE",
                message="the assistant needs a specific item to continue; "
                "choose one on the structured screen",
                reason=REASON_EMPTY_PICKER,
                resolution=resolution,
            )
        metrics.record_resolution(resolution.kind.value, resolution.reason)
        return ContextOutcome(
            updates={
                "context_resolution": _resolution_state(
                    resolution.kind.value, resolution.reason, resolution
                ),
                "active_context": None,
            },
            answer={"cards": [picker_card(resolution.options).model_dump(mode="json")]},
        )

    # NOT_FOUND — every scope-mismatch / missing-provenance / missing-version
    # reason lands here. The reason token goes to TELEMETRY, never into the
    # user-visible message (no tenant identifier is ever echoed back).
    return _fail(
        metrics,
        code="CONTEXT_NOT_FOUND",
        message="the assistant could not identify the item you meant; "
        "use the structured screen",
        reason=resolution.reason,
        resolution=resolution,
    )


def _fail(
    metrics: ContextResolutionMetrics,
    *,
    code: str,
    message: str,
    reason: str,
    resolution: Resolution | None = None,
) -> ContextOutcome:
    """Record the outcome and fail closed to the §12.4 structured failure."""
    metrics.record_resolution(ResolutionKind.NOT_FOUND.value, reason)
    return ContextOutcome(
        updates={
            "context_resolution": _resolution_state(
                ResolutionKind.NOT_FOUND.value, reason, resolution
            ),
            "active_context": None,
        },
        failure=screens_failure(code, message),
    )


def _utc_now() -> str:
    """The as-of clock when the producer did not stamp one (RFC 3339 UTC).

    The transport stamps ``now`` at the request boundary, so this fallback only
    covers direct in-process graph calls. The resolver itself never reads a
    clock — it stays pure and table-testable.
    """
    return datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
