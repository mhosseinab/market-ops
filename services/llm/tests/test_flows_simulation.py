"""Simulation carries no approval control (CHAT-032).

A simulation result is labelled non-executable and structurally cannot carry an
approval control: the type has no control field and the only action-shaped slot
(Recommendation) is deep-link-only guidance keyed to state.simulation.
"""

from __future__ import annotations

from llm.envelope.contract import (
    Calculation,
    Provenance,
    SourcedValue,
    SourceRef,
)
from llm.envelope.models import Money
from llm.flows.simulation import SIMULATION_STATE_KEY, build_simulation


def _engine_calc() -> Calculation:
    return Calculation(
        label="what if margin",
        result=SourcedValue(
            source=SourceRef(tool="read_margin", response_field="contribution.total"),
            provenance=Provenance.MARGIN_ENGINE,
            money=Money(mantissa=120000, currency="IRR", exponent=0),
        ),
    )


def test_simulation_is_labelled_non_executable() -> None:
    sim = build_simulation(model_inference="a what-if only", calculations=[_engine_calc()])
    assert sim.simulation is True
    assert sim.carries_approval_control() is False
    assert sim.envelope.recommendation is not None
    assert sim.envelope.recommendation.state_key == SIMULATION_STATE_KEY


def test_simulation_recommendation_carries_no_authority() -> None:
    sim = build_simulation(calculations=[_engine_calc()])
    rec = sim.envelope.recommendation
    assert rec is not None
    # Only a deep link — never a control/authority token.
    assert rec.deep_link == "/app/screens"
    # The Recommendation model has no field that could approve/execute.
    assert set(rec.model_dump().keys()) == {"statement", "deep_link", "state_key"}


def test_simulation_result_has_no_control_field() -> None:
    sim = build_simulation()
    # Structural: the serialized simulation exposes only simulation + envelope.
    assert set(sim.model_dump().keys()) == {"simulation", "envelope"}
