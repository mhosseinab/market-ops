"""The single deep-link map for the model plane (design/IA_AND_COMPONENTS.md).

Chat never owns a structured control; it points at one via a deep link. Every
route id here mirrors the design's route keys and deep-link map — so a route
rename happens in ONE place, not scattered per flow module. These are route
identifiers (LTR technical ids), not localized copy, so they live in code, not
the locale catalog.
"""

from __future__ import annotations

# Web app route prefix. The web shell mounts the six areas under this base.
_APP = "/app"

# Primary areas (design "Navigation").
ROUTE_TODAY = f"{_APP}/today"
ROUTE_PRODUCTS = f"{_APP}/products"
ROUTE_MARKET = f"{_APP}/market"
ROUTE_ACTIONS = f"{_APP}/actions"
ROUTE_SETTINGS = f"{_APP}/settings"
ROUTE_OPERATIONS = f"{_APP}/operations"

# Sub-routes reached via deep links (design "Deep-link map").
ROUTE_RECOMMENDATION = f"{_APP}/recommendation"
ROUTE_BULK = f"{_APP}/bulk"
ROUTE_COST = f"{_APP}/cost"
ROUTE_DIAGNOSTICS = f"{_APP}/diagnostics"

# The generic screens-only fallback target (§12.4). Kept as the Today workspace,
# the "what requires attention now" surface.
SCREENS_FALLBACK = ROUTE_TODAY


def approval_control(action_id: str) -> str:
    """Deep link to the EXTERNAL structured approval control for an action.

    L4 marketplace mutation happens through the approval card + state machine on
    the actions screen (design "Admin safety levels"). This is the same control
    the screens use; chat only points at it — it never owns a confirm path.
    """
    return f"{ROUTE_ACTIONS}?card={action_id}"


def bulk_control(selection_set_id: str) -> str:
    """Deep link to the bulk selection-set screen (no chat bulk approval)."""
    return f"{ROUTE_BULK}?set={selection_set_id}"


def level2_control(config_id: str) -> str:
    """Deep link to the Level-2 reversible-config confirmation on Settings."""
    return f"{ROUTE_SETTINGS}?confirm={config_id}"


def level3_explanation() -> str:
    """Deep link to the Level-3 commercial-guardrail screen (explanation only)."""
    return f"{ROUTE_SETTINGS}?section=guardrails"


def cost_entry(entity_id: str) -> str:
    """Deep link to the single-value cost-entry screen for a blocker (CHAT-071)."""
    return f"{ROUTE_COST}?entity={entity_id}"
