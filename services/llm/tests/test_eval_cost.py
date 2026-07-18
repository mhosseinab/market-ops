"""The offline cost estimator is deterministic (§4.1 unit-economics input).

Cost per conversation is a Gate 0a input. Offline there are no real tokens, so the
estimate is a deterministic char-based proxy — the same corpus yields the same P75
every run, and the paid S35 gate replaces the proxy with measured usage.
"""

from __future__ import annotations

from llm.evals.cost import CostModel, percentile


def test_token_proxy_and_cost_are_deterministic() -> None:
    model = CostModel()
    assert model.estimate_tokens("abcd") == 1
    assert model.estimate_tokens("") == 1  # floor at one token
    c1 = model.conversation_cost(prompt_text="hello there", response_text="a longer response body")
    c2 = model.conversation_cost(prompt_text="hello there", response_text="a longer response body")
    assert c1 == c2
    assert c1 > 0.0


def test_percentile_is_nearest_rank_and_stable() -> None:
    values = [1.0, 2.0, 3.0, 4.0]
    assert percentile(values, 75.0) == 3.0
    assert percentile(values, 100.0) == 4.0
    assert percentile([], 75.0) == 0.0
    # Reordering the input does not change the percentile.
    assert percentile([4.0, 1.0, 3.0, 2.0], 75.0) == 3.0


def test_output_tokens_cost_more_than_input() -> None:
    model = CostModel()
    text = "x" * 400  # ~100 tokens either way
    in_only = model.conversation_cost(prompt_text=text, response_text="")
    out_only = model.conversation_cost(prompt_text="", response_text=text)
    assert out_only > in_only
