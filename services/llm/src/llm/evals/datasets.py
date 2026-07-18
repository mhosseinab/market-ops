"""Loading and typed access for the frozen eval corpus (§12.5).

The fixtures are JSONL authored by ``fixtures/evals/*`` and are the ARTIFACT the
harness loads — never regenerated at run time. This module only reads and lightly
types them; it never mutates a fixture. Counts are asserted against the §12.5
targets so a truncated corpus fails loud rather than silently under-measuring.
"""

from __future__ import annotations

import json
from dataclasses import dataclass
from pathlib import Path
from typing import Any

# The committed corpus root: ``services/llm/fixtures/evals``.
FIXTURES_ROOT = Path(__file__).resolve().parents[3] / "fixtures" / "evals"

# §12.5 counts (plus the 20 beyond-minimum injection cases). Most suites must match
# EXACTLY — an under-count is a corpus regression, not a soft warning. §12.5 states
# the adversarial set as "at least 50", so it is a MINIMUM (S23 authored 60).
EXPECTED_COUNTS: dict[str, int] = {
    "intents": 200,
    "context": 100,
    "pricing": 100,
    "data_quality": 50,
    "boundary": 50,
    "listing": 50,
    "currency": 30,
    "injection": 20,
}

# Suites whose §12.5 count is a lower bound ("at least N"), not an exact target.
MIN_COUNTS: dict[str, int] = {
    "adversarial": 50,
}

# The four factual-support suites scored through the §12.2 envelope path.
FACTUAL_SUITES: tuple[str, ...] = ("pricing", "data_quality", "boundary", "listing")


def _read_jsonl(path: Path) -> list[dict[str, Any]]:
    with path.open(encoding="utf-8") as fh:
        return [json.loads(line) for line in fh if line.strip()]


def _read_dir(subdir: str) -> list[dict[str, Any]]:
    root = FIXTURES_ROOT / subdir
    rows: list[dict[str, Any]] = []
    for path in sorted(root.glob("*.jsonl")):
        rows.extend(_read_jsonl(path))
    return rows


@dataclass(frozen=True)
class Corpus:
    """The loaded, typed eval corpus. Field names mirror the §12.5 suite names."""

    intents: list[dict[str, Any]]
    context: list[dict[str, Any]]
    adversarial: list[dict[str, Any]]
    pricing: list[dict[str, Any]]
    data_quality: list[dict[str, Any]]
    boundary: list[dict[str, Any]]
    listing: list[dict[str, Any]]
    currency: list[dict[str, Any]]
    injection: list[dict[str, Any]]

    def factual(self) -> list[dict[str, Any]]:
        """Every factual-support case (pricing/data-quality/boundary/listing)."""
        return [*self.pricing, *self.data_quality, *self.boundary, *self.listing]

    def counts(self) -> dict[str, int]:
        return {
            "intents": len(self.intents),
            "context": len(self.context),
            "adversarial": len(self.adversarial),
            "pricing": len(self.pricing),
            "data_quality": len(self.data_quality),
            "boundary": len(self.boundary),
            "listing": len(self.listing),
            "currency": len(self.currency),
            "injection": len(self.injection),
        }


def load_corpus(*, strict_counts: bool = True) -> Corpus:
    """Load the whole eval corpus from the committed fixtures.

    ``strict_counts`` (default) rejects a corpus whose suite counts drifted from
    the §12.5 targets — the harness must measure the full set, never a subset.
    """
    corpus = Corpus(
        intents=_read_dir("intents"),
        context=_read_dir("context"),
        adversarial=_read_dir("adversarial"),
        pricing=_read_dir("pricing"),
        data_quality=_read_dir("data_quality"),
        boundary=_read_dir("boundary"),
        listing=_read_dir("listing"),
        currency=_read_dir("currency"),
        injection=_read_dir("injection"),
    )
    if strict_counts:
        actual = corpus.counts()
        drift = {
            k: (actual[k], EXPECTED_COUNTS[k])
            for k in EXPECTED_COUNTS
            if actual[k] != EXPECTED_COUNTS[k]
        }
        if drift:
            raise ValueError(
                f"eval corpus count drift (actual, expected): {drift} — "
                "the §12.5 suite counts must match exactly"
            )
        under = {k: (actual[k], MIN_COUNTS[k]) for k in MIN_COUNTS if actual[k] < MIN_COUNTS[k]}
        if under:
            raise ValueError(
                f"eval corpus under minimum (actual, minimum): {under} — "
                "§12.5 requires at least this many cases"
            )
    return corpus
