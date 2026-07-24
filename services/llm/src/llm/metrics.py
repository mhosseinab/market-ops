"""Never-cut invariant telemetry for the LLM plane (CLAUDE.md observability).

The free-text-containment boundary MUST be observable (§8, §12.3): every time the
plane routes an approve/execute/confirm *attempt* to guidance-only instead of a
transition, a counter fires and a structured log line is emitted. The field names
mirror the Go core's telemetry schema so fixtures and prod share keys, and the
diagnostic identifiers are locale-neutral — the intent class and disposition are
stable machine tokens, never the user's Persian/English free text (which is never
logged as a diagnostic identifier).

The counter is a simple in-process integer surfaced for tests and startup
logging; the OTel/metrics exporter wiring is the platform's concern. The point is
that the boundary *emits* — telemetry can distinguish a contained approval attempt
from a silent bypass, so the seam is complete.
"""

from __future__ import annotations

import logging
from dataclasses import dataclass, field

# Stable metric/log identifiers (shared schema; locale-neutral).
FREE_TEXT_CONTAINMENT_METRIC = "llm_free_text_containment_total"
CONTEXT_RESOLUTION_METRIC = "llm_context_resolution_total"
_LOGGER = logging.getLogger("llm.containment")
_RESOLUTION_LOGGER = logging.getLogger("llm.contextres")


@dataclass
class ContainmentMetrics:
    """In-process counters for the free-text-containment boundary.

    ``by_intent`` counts contained attempts per guidance-only intent class, so a
    dashboard can show approve-attempts vs confirm-attempts separately. ``total``
    is the sum. No message text is ever stored — only the machine intent token.
    """

    total: int = 0
    by_intent: dict[str, int] = field(default_factory=dict)

    def record_containment(self, intent: str) -> None:
        """Increment the counter and emit the structured log for one containment.

        ``intent`` is a stable machine token (e.g. ``"ApproveAction"``), never the
        user's free text. Logs carry no PII, no marketplace text, no locale copy.
        """
        self.total += 1
        self.by_intent[intent] = self.by_intent.get(intent, 0) + 1
        _LOGGER.info(
            "free_text_containment",
            extra={
                "metric": FREE_TEXT_CONTAINMENT_METRIC,
                "intent": intent,
                "disposition": "guidance_only",
                "transitions": 0,
            },
        )


@dataclass
class ContextResolutionMetrics:
    """In-process counters for the deterministic context-resolution boundary.

    Context resolution decides whether a turn has ONE unambiguous subject
    (``resolved``), must render the structured picker (``picker``), or fails
    closed (``not_found``) — plus ``skipped`` when the turn carried no context at
    all. That boundary enforces quarantine-over-inference (§4.6), so it MUST be
    observable: telemetry has to distinguish a contained ambiguity from a
    silently-guessed subject.

    ``by_outcome`` counts the resolution kinds; ``by_reason`` counts the
    resolver's OWN stable reason tokens (``organization_scope_mismatch``,
    ``ambiguous_reference_card``, ``missing_context_version``, …) — the existing
    machine vocabulary, never an invented synonym. Both are locale-neutral
    machine tokens. No message text, no tenant identifier, no entity id and no
    Persian copy is ever recorded here (CLAUDE.md observability).
    """

    total: int = 0
    by_outcome: dict[str, int] = field(default_factory=dict)
    by_reason: dict[str, int] = field(default_factory=dict)

    def record_resolution(self, outcome: str, reason: str) -> None:
        """Increment the counters and emit the structured log for one resolution."""
        self.total += 1
        self.by_outcome[outcome] = self.by_outcome.get(outcome, 0) + 1
        self.by_reason[reason] = self.by_reason.get(reason, 0) + 1
        _RESOLUTION_LOGGER.info(
            "context_resolution",
            extra={
                "metric": CONTEXT_RESOLUTION_METRIC,
                "outcome": outcome,
                "reason": reason,
            },
        )
