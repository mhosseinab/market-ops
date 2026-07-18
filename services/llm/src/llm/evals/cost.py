"""Deterministic cost estimator for the conversation mix (§4.1 unit economics).

Cost per conversation is a Gate 0a exit input. In the OFFLINE harness there are no
real tokens (the mock/hostile endpoints are free), so this module produces a
DETERMINISTIC estimate from a character-based token proxy and a configurable price
table. It is explicitly an ESTIMATE pending the deferred S35 paid benchmark, which
replaces the proxy with measured provider usage — the harness records the P75 so
the two are comparable at the gate.

The estimate is stable and reproducible (no wall clock, no randomness): the same
corpus yields the same P75 every run, so a regression in prompt/response size is
visible in the measurement log.
"""

from __future__ import annotations

import math
from dataclasses import dataclass

# ~4 characters per token is the conventional OpenAI-family proxy; used only for a
# deterministic offline estimate, never presented as a measured token count.
_CHARS_PER_TOKEN = 4

# Default price table (USD per 1K tokens). Data only — the paid gate overrides it
# with the selected provider pair's real rates before recording the authoritative
# P75. Kept intentionally generic (no vendor branch).
DEFAULT_INPUT_PER_1K = 0.00015
DEFAULT_OUTPUT_PER_1K = 0.00060


@dataclass(frozen=True)
class CostModel:
    """A deterministic per-1K-token price model (offline estimate)."""

    input_per_1k: float = DEFAULT_INPUT_PER_1K
    output_per_1k: float = DEFAULT_OUTPUT_PER_1K

    def estimate_tokens(self, text: str) -> int:
        """Deterministic token proxy for one text (never a measured count)."""
        return max(1, math.ceil(len(text) / _CHARS_PER_TOKEN))

    def conversation_cost(self, *, prompt_text: str, response_text: str) -> float:
        """Estimated USD cost of one conversation turn (input + output)."""
        in_tokens = self.estimate_tokens(prompt_text)
        out_tokens = self.estimate_tokens(response_text)
        return in_tokens / 1000 * self.input_per_1k + out_tokens / 1000 * self.output_per_1k


def percentile(values: list[float], pct: float) -> float:
    """Nearest-rank percentile (deterministic; no interpolation surprises).

    ``pct`` in [0, 100]. Empty input yields 0.0. The nearest-rank method keeps the
    result on an actual observed sample so P75 is reproducible across runs.
    """
    if not values:
        return 0.0
    ordered = sorted(values)
    rank = max(1, math.ceil(pct / 100 * len(ordered)))
    return ordered[min(rank, len(ordered)) - 1]
