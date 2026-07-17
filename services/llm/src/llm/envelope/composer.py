"""Compose a grounded §12.2 response — or fail closed to a structured refusal.

The composer is the seam where typed service data and the model's natural
language become one operational response. It enforces the plane's core rule
positionally: **the model's text can only enter the** ``model_inference`` **slot**
— every category-separated field is built from typed inputs the caller already
holds. Then it runs the grounding walker; if the envelope is not grounded (a
fabricated number, missing evidence, an oversized table, a non-canonical state,
an incomplete comparison, a non-margin exposure), it does NOT emit a
plausible-looking guess: it returns a :class:`CannotAnswer` naming the canonical
reason and a deep link to the structured screen (CHAT-005, §12.4).
"""

from __future__ import annotations

from llm.envelope.contract import (
    Calculation,
    CannotAnswer,
    Claim,
    Comparison,
    ExposureTotal,
    InlineTable,
    Recommendation,
    ResponseEnvelope,
)
from llm.envelope.grounding import GroundingError, Violation, validate_grounding

# The structured screen the plane deep-links to when it cannot answer in chat.
# Aligned with the graph's §12.4 fallback deep link.
FALLBACK_DEEP_LINK = "/app/screens"

# Canonical catalog key for the degraded/cannot-answer copy (packages/locale).
CANNOT_ANSWER_REASON_KEY = "state.degraded.body"


def compose(
    *,
    model_inference: str = "",
    observed_facts: list[Claim] | None = None,
    dk_signals: list[Claim] | None = None,
    seller_config: list[Claim] | None = None,
    deterministic_calculations: list[Calculation] | None = None,
    missing_data: list[str] | None = None,
    recommendation: Recommendation | None = None,
    comparisons: list[Comparison] | None = None,
    tables: list[InlineTable] | None = None,
    exposure: ExposureTotal | None = None,
) -> ResponseEnvelope:
    """Build and VALIDATE a response envelope.

    ``model_inference`` is the only slot the model authors; every other argument
    is typed, sourced data the caller assembled from service responses. Raises
    :class:`GroundingError` if the result is not grounded — callers that must
    fail closed use :func:`compose_or_refuse`.
    """
    env = ResponseEnvelope(
        observed_facts=observed_facts or [],
        dk_signals=dk_signals or [],
        seller_config=seller_config or [],
        deterministic_calculations=deterministic_calculations or [],
        model_inference=model_inference,
        missing_data=missing_data or [],
        recommendation=recommendation,
        comparisons=comparisons or [],
        tables=tables or [],
        exposure=exposure,
    )
    validate_grounding(env)
    return env


def fail_closed(
    *,
    message: str,
    missing: list[str] | None = None,
    violations: list[str] | None = None,
    deep_link: str = FALLBACK_DEEP_LINK,
    reason_key: str = CANNOT_ANSWER_REASON_KEY,
) -> CannotAnswer:
    """The structured "cannot answer" refusal with a deep link (CHAT-005/§12.4)."""
    return CannotAnswer(
        reason_key=reason_key,
        message=message,
        deep_link=deep_link,
        missing=missing or [],
        violations=violations or [],
    )


def _violation_summary(violations: list[Violation]) -> list[str]:
    return [v.code for v in violations]


def compose_or_refuse(
    *,
    model_inference: str = "",
    observed_facts: list[Claim] | None = None,
    dk_signals: list[Claim] | None = None,
    seller_config: list[Claim] | None = None,
    deterministic_calculations: list[Calculation] | None = None,
    missing_data: list[str] | None = None,
    recommendation: Recommendation | None = None,
    comparisons: list[Comparison] | None = None,
    tables: list[InlineTable] | None = None,
    exposure: ExposureTotal | None = None,
) -> ResponseEnvelope | CannotAnswer:
    """Compose a grounded envelope, or fail closed to a structured refusal.

    On any grounding violation the plane returns :class:`CannotAnswer` — never a
    degraded, plausible-looking answer — carrying the violation codes and any
    named missing data for audit, plus the deep link to the structured screen.
    """
    try:
        return compose(
            model_inference=model_inference,
            observed_facts=observed_facts,
            dk_signals=dk_signals,
            seller_config=seller_config,
            deterministic_calculations=deterministic_calculations,
            missing_data=missing_data,
            recommendation=recommendation,
            comparisons=comparisons,
            tables=tables,
            exposure=exposure,
        )
    except GroundingError as exc:
        return fail_closed(
            message="the assistant cannot answer from the available evidence; "
            "use the structured screen",
            missing=list(missing_data or []),
            violations=_violation_summary(exc.violations),
        )
