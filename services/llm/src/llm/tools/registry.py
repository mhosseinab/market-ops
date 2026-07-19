"""The model-visible tool registry — read + Draft-only, nothing else.

This is the single source of truth for what the model can invoke (PRD §12.1,
§12.3, CHAT-003, CHAT-062). It contains ONLY:

* typed **read** tools: catalog, identity, observation, event, margin, policy,
  action, settings — thin, typed wrappers over the generated gateway client
  (``gen/python``); and
* **Draft-only** tools: recommendation-card Draft, Level-2 proposal Draft,
  selection-set Draft — the ONLY writes the model plane may originate (§8.2:
  only *Prepare Action* creates a Draft, and no tool advances an action past
  Draft).

It MUST NEVER contain an approve, execute, confirm-result, guardrail-write, or
permission tool. That is a *structural* prohibition (§12.3), enforced two ways:
:func:`build_registry` refuses to admit a spec whose kind is anything but READ
or DRAFT, and a name-level guard rejects any tool whose name matches a forbidden
verb. The negative test in ``tests/test_registry.py`` asserts both.

Every read/Draft endpoint the wrappers ultimately call lands in later steps
(S21/S23); in S20 the wrappers **fail closed**, returning a structured
"not-yet-wired" DATA payload (never an instruction). Tool results always enter
the model context as untrusted DATA — never as instructions (§12.1): marketplace
content cannot steer the model.
"""

from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass
from enum import StrEnum
from typing import Any

from langchain_core.tools import BaseTool, StructuredTool
from pydantic import BaseModel, ConfigDict, Field


class ToolKind(StrEnum):
    """The only two admissible tool kinds. There is no third kind on purpose."""

    READ = "read"
    DRAFT = "draft"


# Verbs a model-visible tool name may NEVER contain: the structural prohibitions
# of §12.3. A defense-in-depth guard beyond the kind check, so a mis-kinded tool
# still cannot slip through under a state-changing name.
FORBIDDEN_NAME_TOKENS: frozenset[str] = frozenset(
    {
        "approve",
        "execute",
        "confirm",
        "commit",
        "publish",
        "guardrail",
        "permission",
        "grant",
        "floor",  # commercial guardrail write
        "cooldown",
        "movement_cap",
        "override",
        "authorize",
    }
)


class _NoArgs(BaseModel):
    """Default empty args schema (typed boundary; no free-form kwargs)."""

    model_config = ConfigDict(extra="forbid")


@dataclass(frozen=True)
class ToolSpec:
    """Declarative description of one model-visible tool.

    ``perm_action`` mirrors the core ``internal/perm`` action the tool's
    ultimate call authorizes against — a READ tool maps to an L1 read action, a
    DRAFT tool to a ``draft.*`` action. The LLM_GATEWAY_TOKEN can reach exactly
    these (core-side ``perm.GatewayCan``), so the two planes agree by name.
    """

    name: str
    kind: ToolKind
    perm_action: str
    description: str
    args_schema: type[BaseModel] = _NoArgs

    def __post_init__(self) -> None:
        lowered = self.name.lower()
        for token in FORBIDDEN_NAME_TOKENS:
            if token in lowered:
                raise ValueError(
                    f"tool {self.name!r} name contains forbidden state-changing token "
                    f"{token!r} (§12.3 structural prohibition)"
                )
        if self.kind not in (ToolKind.READ, ToolKind.DRAFT):
            raise ValueError(f"tool {self.name!r} has non read/Draft kind {self.kind!r}")


# --- read tool arg schemas (typed boundaries) --------------------------------


class _AccountArg(BaseModel):
    model_config = ConfigDict(extra="forbid")
    marketplace_account_id: str = Field(description="Account context (UUID).")


class _EntityArg(BaseModel):
    model_config = ConfigDict(extra="forbid")
    marketplace_account_id: str = Field(description="Account context (UUID).")
    entity_id: str = Field(description="Resolved entity id (UUID).")


class _DraftRecommendationArg(BaseModel):
    model_config = ConfigDict(extra="forbid")
    marketplace_account_id: str
    recommendation_id: str = Field(description="Recommendation version to draft from.")


class _DraftLevel2Arg(BaseModel):
    model_config = ConfigDict(extra="forbid")
    marketplace_account_id: str
    setting_key: str = Field(description="Reversible L2 setting being proposed.")


class _DraftSelectionSetArg(BaseModel):
    model_config = ConfigDict(extra="forbid")
    marketplace_account_id: str
    query: str = Field(description="Deterministic filter defining the set.")


# The declarative registry (read tools then Draft tools). Adding a tool here is a
# reviewed change; the kind + name guards make an unsafe addition fail closed.
_READ_TOOLS: tuple[ToolSpec, ...] = (
    ToolSpec(
        "read_catalog",
        ToolKind.READ,
        "read.current_strategy",
        "Read owned catalog entities for an account.",
        _AccountArg,
    ),
    ToolSpec(
        "read_identity",
        ToolKind.READ,
        "connector.inspect",
        "Read the versioned market-product identity mapping.",
        _EntityArg,
    ),
    ToolSpec(
        "read_observation",
        ToolKind.READ,
        "read.connection_status",
        "Read observed offers / evidence for an entity.",
        _EntityArg,
    ),
    ToolSpec(
        "read_event",
        ToolKind.READ,
        "read.connection_status",
        "Read market-event lifecycle records.",
        _AccountArg,
    ),
    ToolSpec(
        "read_margin",
        ToolKind.READ,
        "read.cost_readiness",
        "Read margin-engine snapshot outputs (engine numbers only).",
        _EntityArg,
    ),
    ToolSpec(
        "read_policy",
        ToolKind.READ,
        "read.current_strategy",
        "Read policy-engine outputs (recommendations, guardrail state).",
        _EntityArg,
    ),
    ToolSpec(
        "read_action",
        ToolKind.READ,
        "read.connection_status",
        "Read append-only action state history (read-only).",
        _AccountArg,
    ),
    ToolSpec(
        "read_settings",
        ToolKind.READ,
        "read.current_strategy",
        "Read Level-1 settings values (connection, readiness, strategy).",
        _AccountArg,
    ),
)

_DRAFT_TOOLS: tuple[ToolSpec, ...] = (
    ToolSpec(
        "draft_recommendation",
        ToolKind.DRAFT,
        "draft.recommendation",
        "Create a recommendation-card Draft (never advances past Draft).",
        _DraftRecommendationArg,
    ),
    ToolSpec(
        "draft_level2_proposal",
        ToolKind.DRAFT,
        "draft.level2_proposal",
        "Create a Level-2 reversible-config proposal Draft.",
        _DraftLevel2Arg,
    ),
    ToolSpec(
        "draft_selection_set",
        ToolKind.DRAFT,
        "draft.selection_set",
        "Create a named, versioned bulk selection-set Draft.",
        _DraftSelectionSetArg,
    ),
)

READ_TOOL_NAMES: frozenset[str] = frozenset(t.name for t in _READ_TOOLS)
DRAFT_TOOL_NAMES: frozenset[str] = frozenset(t.name for t in _DRAFT_TOOLS)


def _stub_runner(spec: ToolSpec, wired_in: str) -> Callable[..., dict[str, Any]]:
    """Build the fail-closed stub for a tool.

    Returns a structured DATA payload (never an instruction, never a plausible
    guess): the endpoint lands in ``wired_in``. This is an explicitly-planned
    fail-closed stub (CLAUDE.md engineering method).
    """

    def run(**kwargs: Any) -> dict[str, Any]:
        return {
            "status": "unavailable",
            "tool": spec.name,
            "kind": spec.kind.value,
            "reason": "tool endpoint is not wired in P0 S20; it fails closed by design",
            "wired_in": wired_in,
            "args_echo": kwargs,
        }

    return run


class ToolRegistry:
    """An immutable set of read/Draft tool specs plus their LangChain tools.

    Agents bind tools ONLY from here (``langchain_tools``); the registry is the
    single source, so the union of every agent's bound tools is a subset of
    :meth:`names` (asserted by the agent-binding test).
    """

    def __init__(
        self,
        specs: tuple[ToolSpec, ...],
        *,
        read_runner_overrides: dict[str, Callable[..., dict[str, Any]]] | None = None,
    ) -> None:
        self._assert_contained(specs)
        self._specs = specs
        self._by_name = {s.name: s for s in specs}
        overrides = read_runner_overrides or {}
        self._assert_overrides_are_reads(overrides, self._by_name)
        self._tools: dict[str, BaseTool] = {
            s.name: StructuredTool.from_function(
                func=overrides.get(s.name, _stub_runner(s, wired_in="S21/S23")),
                name=s.name,
                description=s.description,
                args_schema=s.args_schema,
            )
            for s in specs
        }

    @staticmethod
    def _assert_overrides_are_reads(
        overrides: dict[str, Callable[..., dict[str, Any]]],
        by_name: dict[str, ToolSpec],
    ) -> None:
        """Fail closed: a runner override may only substitute a READ tool's DATA.

        The §12.5 data-channel injection suite (issue #112) needs a READ tool to
        return a *fake authoritative* marketplace read (hostile evidence in a typed
        field) instead of the fail-closed stub, so the injected text traverses the
        real read-tool/provider/agent boundary as untrusted DATA. Only the returned
        DATA changes — the tool's name, KIND, args schema, and the name/kind guards
        are untouched — so a Draft/forbidden tool can never be introduced this way.
        An override that names an unknown tool or a non-READ tool is rejected.
        """
        for name in overrides:
            spec = by_name.get(name)
            if spec is None:
                raise ValueError(
                    f"read_runner_overrides names unknown tool {name!r}; "
                    "an override may only substitute an existing READ tool"
                )
            if spec.kind is not ToolKind.READ:
                raise ValueError(
                    f"read_runner_overrides may only substitute READ tools; "
                    f"{name!r} is {spec.kind.value!r} (§12.3)"
                )

    @staticmethod
    def _assert_contained(specs: tuple[ToolSpec, ...]) -> None:
        """Fail closed at construction: every spec is READ or DRAFT only."""
        seen: set[str] = set()
        for s in specs:
            if s.name in seen:
                raise ValueError(f"duplicate tool name {s.name!r} in registry")
            seen.add(s.name)
            if s.kind not in (ToolKind.READ, ToolKind.DRAFT):
                raise ValueError(
                    f"registry rejected {s.name!r}: only READ and DRAFT tools are admissible "
                    f"(§12.3 CHAT-003) — got {s.kind!r}"
                )

    def specs(self) -> tuple[ToolSpec, ...]:
        return self._specs

    def names(self) -> frozenset[str]:
        return frozenset(self._by_name)

    def spec(self, name: str) -> ToolSpec:
        return self._by_name[name]

    def langchain_tools(self) -> list[BaseTool]:
        return list(self._tools.values())

    def tool(self, name: str) -> BaseTool:
        return self._tools[name]

    def manifest(self) -> dict[str, Any]:
        """The registry manifest served by ``GET /registry/manifest``.

        Its ``tools`` list is the authoritative catalogue the containment test
        and the agent-binding test both check against.
        """
        return {
            "version": "s20",
            "kinds": [k.value for k in ToolKind],
            "tools": [
                {
                    "name": s.name,
                    "kind": s.kind.value,
                    "perm_action": s.perm_action,
                    "description": s.description,
                }
                for s in self._specs
            ],
        }


def gateway_envelope_manifest() -> dict[str, Any]:
    """The LLM_GATEWAY_TOKEN capability envelope, derived from THIS registry.

    This is the single cross-language source of truth for the machine
    credential's authority (issue #26, PRD §12.3): the machine principal may
    reach EXACTLY the unique ``perm_action`` values of the typed READ tools plus
    the ``draft.*`` actions of the DRAFT tools — nothing more. In particular it
    can never contain a human-facing session/surface action (``session.read``,
    ``session.logout``, ``chat.converse``): no such tool exists in the registry,
    so no such action can appear here.

    The committed ``contracts/llm_gateway_envelope.json`` is generated from this
    function; the Go core asserts its machine envelope equals that file, and the
    Python test ``test_gateway_envelope_manifest.py`` asserts this function
    equals that file. The pair fails CLOSED when either plane drifts: a new/renamed
    tool perm_action changes this manifest (and the committed file, and the Go
    envelope must follow), and a widened Go envelope breaks against the file.
    """
    registry = build_registry()
    read_actions = sorted(
        {s.perm_action for s in registry.specs() if s.kind is ToolKind.READ}
    )
    draft_actions = sorted(
        {s.perm_action for s in registry.specs() if s.kind is ToolKind.DRAFT}
    )
    return {
        "version": "s20",
        "read_actions": read_actions,
        "draft_actions": draft_actions,
    }


def build_registry(
    *, read_runner_overrides: dict[str, Callable[..., dict[str, Any]]] | None = None
) -> ToolRegistry:
    """Build the canonical P0 registry (read tools + Draft-only tools).

    ``read_runner_overrides`` is an EVAL-ONLY seam (issue #112): it substitutes the
    DATA a named READ tool returns with a fake authoritative marketplace read, so
    the §12.5 data-channel injection suite can drive hostile evidence through the
    real read-tool/provider/agent boundary. It changes only the returned payload;
    every structural guard (kind check, forbidden-name guard) still applies.
    """
    return ToolRegistry(
        _READ_TOOLS + _DRAFT_TOOLS, read_runner_overrides=read_runner_overrides
    )
