"""Containment tests for the model tool registry (PRD §12.3, CHAT-003/CHAT-062).

These are the safety-critical negative tests. They assert the registry holds
ONLY read and Draft-only tools, that NO tool can move an action past Draft, that
no approve/execute/confirm/guardrail/permission tool exists, and that the union
of every agent's bound tools is a subset of the registry manifest.
"""

from __future__ import annotations

import pytest
from llm.config import ProviderKind, Settings
from llm.orchestrator.agent import build_agent
from llm.providers.base import build_chat_model
from llm.providers.mock import MockScript
from llm.tools.registry import (
    FORBIDDEN_NAME_TOKENS,
    ToolKind,
    ToolRegistry,
    ToolSpec,
    build_registry,
)

# The state-changing tool categories §12.3 forbids the model plane from holding.
FORBIDDEN_KINDS = {"approve", "execute", "confirm", "confirm_result", "guardrail", "permission"}


def test_registry_holds_only_read_and_draft_tools() -> None:
    registry = build_registry()
    for spec in registry.specs():
        assert spec.kind in (ToolKind.READ, ToolKind.DRAFT), (
            f"tool {spec.name!r} is neither READ nor DRAFT — §12.3 breach"
        )


def test_no_state_changing_tool_exists() -> None:
    """No tool can move an action past Draft; no forbidden tool type exists."""
    registry = build_registry()
    manifest = registry.manifest()

    # The manifest advertises only the two admissible kinds.
    assert set(manifest["kinds"]) == {"read", "draft"}

    for tool in manifest["tools"]:
        assert tool["kind"] in ("read", "draft")
        # No forbidden kind label leaked into the manifest.
        assert tool["kind"] not in FORBIDDEN_KINDS
        name = tool["name"].lower()
        for token in FORBIDDEN_NAME_TOKENS:
            assert token not in name, (
                f"tool {tool['name']!r} carries forbidden state-changing token {token!r}"
            )
        # A Draft tool's perm action stays within draft.*; a read tool never
        # names an approve/execute/guardrail/permission action.
        action = tool["perm_action"]
        assert not action.startswith(("price.", "guardrail."))
        assert action not in {"price.approve", "price.execute"}


def test_spec_rejects_a_non_read_or_draft_kind() -> None:
    """A spec whose kind is not READ/DRAFT fails closed at construction."""
    with pytest.raises(ValueError):
        ToolSpec("read_ok", "mutate", "read.x", "smuggled non read/Draft kind")  # type: ignore[arg-type]


def test_registry_rejects_duplicate_tool_names() -> None:
    with pytest.raises(ValueError):
        ToolRegistry(
            (
                ToolSpec("read_dup", ToolKind.READ, "read.x", "a"),
                ToolSpec("read_dup", ToolKind.READ, "read.y", "b"),
            )
        )


def test_forbidden_named_tool_is_rejected() -> None:
    """A tool named with a forbidden verb is rejected even if kinded READ."""
    with pytest.raises(ValueError):
        ToolSpec("approve_price", ToolKind.READ, "read.x", "should never exist")
    with pytest.raises(ValueError):
        ToolSpec("execute_change", ToolKind.DRAFT, "draft.x", "should never exist")
    with pytest.raises(ValueError):
        ToolSpec("set_contribution_floor", ToolKind.DRAFT, "draft.x", "guardrail write")


def test_all_agents_bound_tools_are_subset_of_manifest() -> None:
    """Union of every agent's bound tools ⊆ the registry manifest (single source)."""
    registry = build_registry()
    manifest_names = {t["name"] for t in registry.manifest()["tools"]}
    settings = Settings(provider_kind=ProviderKind.MOCK)
    model = build_chat_model(settings, mock_script=MockScript(mode="say"))

    # A few representative agents binding different subsets — S20 has one turn
    # agent; post-P0 specialists join. The invariant holds for every one.
    agents = [
        build_agent(model, registry, settings),  # binds all
        build_agent(model, registry, settings, bind=frozenset({"read_margin", "read_policy"})),
        build_agent(model, registry, settings, bind=frozenset({"draft_recommendation"})),
    ]
    union: set[str] = set()
    for handle in agents:
        union |= set(handle.bound_tool_names)
        assert handle.bound_tool_names <= manifest_names

    assert union <= manifest_names


def test_agent_cannot_bind_a_tool_outside_the_registry() -> None:
    registry = build_registry()
    settings = Settings(provider_kind=ProviderKind.MOCK)
    model = build_chat_model(settings, mock_script=MockScript(mode="say"))
    with pytest.raises(ValueError):
        build_agent(model, registry, settings, bind=frozenset({"approve_everything"}))
