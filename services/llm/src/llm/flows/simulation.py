"""Simulation (Journey 8, PRD §6.9, CHAT-032). Non-executable by construction.

A simulation calls the S16 engines (policy evaluation with ``Simulation=true``)
and relays the result labelled non-executable. NO simulation message ever carries
an approval control (CHAT-032) — and structurally it *cannot*: a
:class:`SimulationResult` has no control field, and the only action-shaped slot it
exposes (:class:`~llm.envelope.contract.Recommendation`) carries a deep link, not
authority. The ``state.simulation`` glossary key marks every simulation output.
"""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict

from llm.envelope.contract import Calculation, Recommendation, ResponseEnvelope
from llm.flows.deep_links import SCREENS_FALLBACK

SIMULATION_STATE_KEY = "state.simulation"


class SimulationResult(BaseModel):
    """A labelled what-if result. ``simulation`` is always True; there is no
    control field and no way to add one — a simulation is never approvable.
    """

    model_config = ConfigDict(extra="forbid")

    simulation: bool = True
    envelope: ResponseEnvelope

    def carries_approval_control(self) -> bool:
        """Always False: a simulation is structurally non-executable (CHAT-032)."""
        return False


def build_simulation(
    *,
    model_inference: str = "",
    calculations: list[Calculation] | None = None,
    note_key: str | None = None,
) -> SimulationResult:
    """Assemble a simulation result from engine outputs. Pure.

    ``calculations`` are engine (margin/pricing) figures the caller fetched; the
    recommendation, if any, is guidance labelled ``state.simulation`` with only a
    deep link — never a control. The envelope is grounded by the composer's rules
    when emitted; here we shape a non-executable result.
    """
    recommendation = Recommendation(
        statement="",
        deep_link=SCREENS_FALLBACK,
        state_key=SIMULATION_STATE_KEY,
    )
    envelope = ResponseEnvelope(
        deterministic_calculations=calculations or [],
        model_inference=model_inference,
        recommendation=recommendation,
        missing_data=[note_key] if note_key else [],
    )
    return SimulationResult(simulation=True, envelope=envelope)
