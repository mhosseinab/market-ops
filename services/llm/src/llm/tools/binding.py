"""Capability-bounded tool binding (PRD §8.2, §12.3, CHAT-003; issue #31).

The binding layer is the deterministic consumer that turns a per-intent
:class:`~llm.intents.capabilities.IntentCapability` into the concrete set of tool
names an intent may bind against a :class:`~llm.tools.registry.ToolRegistry`.

Its one guarantee is that it can NEVER widen authority. The bound set is bounded
three ways, all intersections and never a union:

* it is a subset of the intent's OWN capability names (the intent governs — a
  caller cannot substitute a more permissive capability);
* it is a subset of the registry (a tool the registry does not hold cannot bind);
* each bound tool's registry KIND must be in the capability's ``allowed_kinds``
  (so a Draft tool is inert for any intent not granted the DRAFT kind — the
  never-cut rule that only Prepare Action originates a Draft, §8.2).

An optional ``requested`` selector can only NARROW the result (it is intersected
with the intent's grant), so it too cannot widen authority.
"""

from __future__ import annotations

from collections.abc import Iterable

from llm.intents.capabilities import capability_for
from llm.intents.models import IntentClass
from llm.tools.registry import ToolRegistry


def bind_tools_for_intent(
    intent: IntentClass,
    registry: ToolRegistry,
    requested: Iterable[str] | None = None,
) -> frozenset[str]:
    """Resolve the tool names ``intent`` may bind against ``registry``.

    The intent's own capability governs and cannot be overridden. ``requested``,
    if given, only narrows the result (intersection). The result is always a
    subset of the capability names, the registry, and the capability's allowed
    kinds — the binding layer never adds a tool the capability does not grant.
    """
    capability = capability_for(intent)
    candidate = capability.tool_names
    if requested is not None:
        candidate = candidate & frozenset(requested)

    registry_names = registry.names()
    bound = {
        name
        for name in candidate
        if name in registry_names
        and registry.spec(name).kind in capability.allowed_kinds
    }
    return frozenset(bound)
