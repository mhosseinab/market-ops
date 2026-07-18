"""Investigation filters (Journey 7, PRD §6.8, CHAT-033). Deterministic.

A conversational filter ("blocked products below floor, sorted by exposure")
compiles to a :class:`FilterSpec`, and a FilterSpec serializes to a canonical
query string via :func:`compile_query`. The screens build the SAME FilterSpec and
call the SAME serializer, so the chat query and the screen query are byte-equal
by construction (CHAT-033) — chat has no second serializer to drift from.

Determinism, not model improvisation, is the invariant: the serializer sorts keys
and normalizes value order, so two equivalent filters always produce identical
bytes. Unknown filter tokens are rejected (fail closed) rather than guessed.
"""

from __future__ import annotations

from enum import StrEnum
from urllib.parse import urlencode

from pydantic import BaseModel, ConfigDict, Field, field_validator


class FilterField(StrEnum):
    """The filterable dimensions shared by chat and the products/market screens."""

    STATE = "state"  # canonical state key(s)
    BELOW_FLOOR = "below_floor"
    FRESHNESS = "freshness"
    HAS_RECOMMENDATION = "has_recommendation"
    MONITORING_TIER = "monitoring_tier"


class SortKey(StrEnum):
    """The sort dimensions shared by chat and screens."""

    EXPOSURE = "exposure"
    RANK = "rank"
    LAST_OBSERVED = "last_observed"
    MARGIN = "margin"


class SortDir(StrEnum):
    ASC = "asc"
    DESC = "desc"


class FilterSpec(BaseModel):
    """A normalized investigation filter — the single pre-serialization form.

    Chat parses natural language into this; a screen builds it from its controls.
    Both then call :func:`compile_query`, so the resulting query strings are
    byte-identical (CHAT-033). List values are order-insensitive: the serializer
    sorts them, so ``state=[a,b]`` and ``state=[b,a]`` produce the same bytes.
    """

    model_config = ConfigDict(extra="forbid")

    account_id: str
    states: list[str] = Field(default_factory=list)  # canonical state keys
    below_floor: bool | None = None
    freshness: list[str] = Field(default_factory=list)  # canonical freshness keys
    has_recommendation: bool | None = None
    monitoring_tier: str | None = None
    sort_key: SortKey | None = None
    sort_dir: SortDir = SortDir.DESC
    page_size: int = 20

    @field_validator("page_size")
    @classmethod
    def _bounded_page(cls, v: int) -> int:
        if not 1 <= v <= 200:
            raise ValueError("page_size out of bounds")
        return v


def compile_query(spec: FilterSpec) -> str:
    """Serialize a FilterSpec to the canonical query string. Pure and stable.

    THE single serializer for both surfaces (CHAT-033). Rules that make it
    canonical: pairs are emitted in a fixed key order; list values are sorted and
    comma-joined; booleans render as ``true``/``false``; unset optionals are
    omitted. Two FilterSpecs that are semantically equal serialize identically.
    """
    params: list[tuple[str, str]] = [("account", spec.account_id)]

    if spec.states:
        params.append(("state", ",".join(sorted(spec.states))))
    if spec.below_floor is not None:
        params.append(("below_floor", "true" if spec.below_floor else "false"))
    if spec.freshness:
        params.append(("freshness", ",".join(sorted(spec.freshness))))
    if spec.has_recommendation is not None:
        params.append(
            ("has_recommendation", "true" if spec.has_recommendation else "false")
        )
    if spec.monitoring_tier is not None:
        params.append(("monitoring_tier", spec.monitoring_tier))
    if spec.sort_key is not None:
        params.append(("sort", f"{spec.sort_key.value}:{spec.sort_dir.value}"))
    params.append(("page_size", str(spec.page_size)))

    # Deterministic key order regardless of construction order.
    params.sort(key=lambda kv: kv[0])
    return urlencode(params, safe=",:")
