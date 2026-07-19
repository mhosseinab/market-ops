"""Cross-language drift test for the LLM machine-credential envelope (issue #26).

The committed ``contracts/llm_gateway_envelope.json`` is the single shared
statement of the LLM_GATEWAY_TOKEN capability envelope. The Go core asserts its
machine grant set equals that file (perm/gateway_manifest_test.go); this Python
half asserts the file still equals what the LIVE typed tool registry declares.
Together they fail CLOSED when either plane changes independently: a new/renamed
tool perm_action here makes the live manifest diverge from the committed file
(this test fails) until the file — and the Go envelope — are regenerated to match.

It also pins the never-cut invariant directly (PRD §8/§12.3): no human-facing
session/surface action may ever enter the machine envelope.
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any, cast

from llm.tools.registry import gateway_envelope_manifest

# services/llm/tests/<this file> -> parents[3] == repo root.
_MANIFEST_PATH = (
    Path(__file__).resolve().parents[3] / "contracts" / "llm_gateway_envelope.json"
)

# Human-facing session/surface L1 actions that must NEVER be in the machine
# envelope — no typed model-visible tool declares them (issue #26).
_FORBIDDEN_SURFACE_ACTIONS = frozenset(
    {"session.read", "session.logout", "chat.converse"}
)


def _committed_manifest() -> dict[str, Any]:
    return cast("dict[str, Any]", json.loads(_MANIFEST_PATH.read_text(encoding="utf-8")))


def test_committed_manifest_matches_live_registry() -> None:
    """The committed cross-language manifest equals the live registry envelope."""
    assert _committed_manifest() == gateway_envelope_manifest(), (
        "contracts/llm_gateway_envelope.json is stale: regenerate it from "
        "gateway_envelope_manifest() and update the Go machine envelope to match "
        "(issue #26 drift guard)."
    )


def test_manifest_excludes_all_session_and_surface_actions() -> None:
    """No human-facing session/surface action may enter the machine envelope."""
    manifest = gateway_envelope_manifest()
    all_actions = set(manifest["read_actions"]) | set(manifest["draft_actions"])
    leaked = all_actions & _FORBIDDEN_SURFACE_ACTIONS
    assert not leaked, f"machine envelope must not contain surface actions: {leaked}"


def test_manifest_read_actions_are_exactly_the_typed_read_tool_actions() -> None:
    """Read envelope == the unique perm_action values of the typed READ tools."""
    manifest = gateway_envelope_manifest()
    assert manifest["read_actions"] == sorted(
        {
            "connector.inspect",
            "read.connection_status",
            "read.cost_readiness",
            "read.current_strategy",
        }
    )
    assert manifest["draft_actions"] == sorted(
        {
            "draft.recommendation",
            "draft.level2_proposal",
            "draft.selection_set",
        }
    )
