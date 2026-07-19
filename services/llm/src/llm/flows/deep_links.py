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

# The closed set of internal recovery routes a fail-closed response may deep-link
# to (§12.4 structured failure / cannot-answer refusal). A failure or refusal
# routes the user to a DETERMINISTIC internal screen — never a model-authored
# path, an external URL, or any surface outside this set. Membership is EXACT:
# an external scheme (``https:``, ``javascript:``), a host or protocol-relative
# form (``//host``), path traversal (``../``), an encoded bypass, or an unknown
# internal path is not in the set and therefore fails closed (issue #56).
RECOVERY_ROUTES: frozenset[str] = frozenset(
    {
        ROUTE_TODAY,
        ROUTE_PRODUCTS,
        ROUTE_MARKET,
        ROUTE_ACTIONS,
        ROUTE_SETTINGS,
        ROUTE_OPERATIONS,
    }
)


def validate_recovery_route(value: str) -> str:
    """Return ``value`` if it is an approved internal recovery route, else raise.

    The single gate for every fail-closed ``deep_link`` (§12.4). Because the
    check is exact membership in :data:`RECOVERY_ROUTES`, it structurally rejects
    external schemes, hosts, protocol-relative URLs, path traversal, encoded
    bypasses, and any unknown internal path — there is no parsing seam to bypass.
    The rejected value is NOT echoed into the error (free-text containment, §8).
    """
    if value not in RECOVERY_ROUTES:
        raise ValueError(
            "deep_link must be an approved internal recovery route "
            "from the closed RECOVERY_ROUTES set (issue #56)"
        )
    return value


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
