"""FastAPI application for the LLM plane (PRD §12.1, §19.3).

Endpoints:

* ``GET  /healthz``            — liveness.
* ``GET  /registry/manifest``  — the read/Draft-only tool manifest (CHAT-003).
* ``POST /chat``               — a conversation turn streamed as SSE
  (text/event-stream; no WebSocket, §19.3). Streams tokens then a final typed
  envelope, or a §12.4 structured failure. Honors the local kill switch: when
  chat is disabled it returns a structured disabled state and NOTHING else
  degrades — ``/registry/manifest`` and ``/healthz`` stay fully functional
  (CHAT-009).

The app builds one OpenAI-compatible model (mock by default — no paid calls),
one shared registry, one leaf agent bound to that registry, and the LangGraph
turn around it. Graph state is per-request and in-process (no DB credential,
§19.3).
"""

from __future__ import annotations

import hmac
import uuid
from collections.abc import AsyncIterator, Callable
from datetime import UTC, datetime
from typing import Annotated, Any

import httpx
from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, ConfigDict, Field

from llm.config import ProviderKind, Settings, load_settings
from llm.contextres.ports import CandidatePort, NoCandidatePort
from llm.envelope.models import ChatStreamEvent, StreamEventKind
from llm.flows.gateway_draft import GatewayDraftPort
from llm.flows.gateway_read import GatewayReadPort
from llm.flows.ports import DraftPort
from llm.flows.read_ports import NoReadPort, ReadPort
from llm.intents.classifier import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
from llm.intents.models import IntentClass
from llm.metrics import ContainmentMetrics, ContextResolutionMetrics
from llm.observability import configure_observability
from llm.orchestrator.agent import AgentHandle, build_agent
from llm.orchestrator.graph import TurnGraph, TurnState, build_turn_graph
from llm.providers.base import build_chat_model
from llm.providers.mock import MockScript
from llm.tools.binding import bind_tools_for_intent
from llm.tools.registry import ToolRegistry, build_registry
from llm.tools.runners import build_production_read_runners


class ChatRequest(BaseModel):
    """A conversation turn from the gateway. Free text carries no authority.

    ``organization_id`` + ``marketplace_account_id`` are the turn's SCOPE — the
    ONLY source of the context resolver's
    :class:`~llm.contextres.models.RequestScope`. The tenant fields carried
    inside :attr:`context` are untrusted DATA validated against that scope, never
    the scope itself (PRD §12, §4.6 identity quarantine).

    The two are asserted by the gateway under the inbound bearer credential
    (issue #167), but they are NOT equally strong:

    * ``organization_id`` is the caller's authenticated organization;
    * ``marketplace_account_id`` is the account the GATEWAY RESOLVED for the turn
      (``services/core/internal/httpapi/chat.go``: the stored conversation
      governs; a request account contradicting it is denied; an omitted one
      inherits the stored value). It is authoritative in that a caller cannot
      choose it freely — not a bearer-asserted identity in the same sense as the
      organization.

    GAP CLOSED (issue #412, was #108 G3): a NEW conversation could previously be
    opened naming an account owned by ANOTHER organization —
    ``CreateConversation`` performed no org-ownership check and
    ``migrations/0005_conversation.sql`` carried only an FK to
    ``marketplace_accounts(id)`` (existence, not ownership). The gateway now
    scopes the insert by the caller's organization AND enforces ownership in the
    database (``migrations/0048_conversation_account_ownership.sql``: a composite
    ``(marketplace_account_id, organization_id)`` foreign key plus an
    ownership-pair immutability trigger), so a conversation cannot reference a
    foreign account even if application code is bypassed. A conversation's stored
    account is therefore owned by its organization, and the pairing this plane
    receives is a coherent tenant aggregate.

    That does NOT make this plane's checks optional: the scope is still validated
    against the untrusted ``context`` payload here, and an authoritative read in
    108c is still authorized on the gateway side — never on the strength of a
    field this plane received.

    ``extra="ignore"`` is retained DELIBERATELY at this level: the gateway is a
    co-evolving producer that already sends top-level keys this plane does not
    model (``locale``, and more as the contract grows additively), and rejecting
    them would break every turn on an additive producer change.

    :attr:`context` is deliberately UNTYPED here — the transport carries it
    verbatim and :class:`~llm.contextres.turn.TurnContext` (still ``extra=
    "forbid"``) validates it inside ``resolve_turn_context``. Typing it at this
    boundary would make FastAPI reject a malformed payload with a 422 BEFORE the
    resolver ran, and the gateway reads any non-2xx as a transport error
    (``provider_unavailable``): a context contract mismatch would surface as "LLM
    plane down" with no §12.4 structured failure, no screens-only deep link, and
    — worst — no ``llm_context_resolution_total`` event, leaving the fail-closed
    seam invisible in telemetry. Rejection still happens; it happens where it is
    structured, observable and recoverable. A misspelled key inside ``context``
    is still never silently dropped: dropping it would lose the turn's subject.
    """

    model_config = ConfigDict(extra="ignore")

    message: str = Field(min_length=1, max_length=8000)
    conversation_id: str | None = None
    marketplace_account_id: str | None = None
    user_id: str | None = None
    organization_id: str | None = None
    # The gateway's authoritative bound context (CHAT-007). Read-only business
    # data: it carries no approval authority and never advances an action.
    context: dict[str, Any] | None = None


class AppState:
    """Process-wide singletons wired once at startup."""

    def __init__(
        self,
        settings: Settings,
        *,
        http_client: httpx.Client | None = None,
        mock_intent_classifier: Callable[[str], str] | None = None,
    ) -> None:
        self.settings = settings
        self.observability = configure_observability(settings)
        self.metrics = ContainmentMetrics()
        self.resolution_metrics = ContextResolutionMetrics()
        # Candidate supply for explicit entity references. STILL the fail-closed
        # stub: it supplies NOTHING, so an explicit reference resolves to a
        # structured picker or NOT_FOUND — never a guessed subject.
        #
        # Sub-scope 108c wired the authoritative READ and Draft transports below,
        # but NOT this one: an entity-candidate lookup needs a bounded
        # entity-search read that ``contracts/gateway.openapi.yaml`` does not
        # expose, and the contract is held by another lane — so the alternative to
        # keeping this stub would be inventing an endpoint. It stays an
        # explicitly-planned fail-closed stub with its own negative test; the
        # downstream completer is the reviewed contract addition of that read.
        self.candidate_port: CandidatePort = NoCandidatePort()

        # --- authoritative outbound transports (issue #108, 108c) -------------
        # Constructed ONCE here and injected. Under the mock provider (all tests,
        # local dev) the outbound configuration is normally absent, so both ports
        # stay at their fail-closed stubs and NOTHING reaches the network — a
        # test can never make a live or paid call by accident. The production
        # (openai_compatible) transport refuses to start unconfigured.
        self.http_client: httpx.Client | None = None
        self.read_port: ReadPort = NoReadPort()
        self.draft_port: DraftPort | None = None
        base_url = settings.outbound_gateway_base_url()
        token = settings.outbound_gateway_token()
        if base_url is not None and token is not None:
            self.http_client = http_client if http_client is not None else httpx.Client()
            self.read_port = GatewayReadPort(
                base_url,
                token,
                self.http_client,
                timeout_seconds=settings.read_timeout_seconds,
            )
            self.draft_port = GatewayDraftPort(
                base_url,
                token,
                self.http_client,
                timeout_seconds=settings.draft_timeout_seconds,
            )

        # The registry's PRODUCTION runner seam: the model-visible READ tools get
        # the real transport; the three ``draft_*`` tools keep their fail-closed
        # stub, because the seam structurally refuses a DRAFT tool. Draft
        # origination therefore has exactly ONE path — the deterministic
        # Prepare-Action flow (§8.2, issue #108 hazard H2).
        self.business_day = _utc_business_day()
        production_runners = (
            build_production_read_runners(self.read_port, business_day=self.business_day)
            if not isinstance(self.read_port, NoReadPort)
            else None
        )
        self.registry: ToolRegistry = build_registry(
            production_read_runners=production_runners
        )

        # The agent model (answers) and the classifier model are separate roles.
        # In production both resolve to the SAME configured OpenAI-compatible
        # endpoint; with the mock they carry different deterministic scripts so a
        # turn can classify AND answer. The classifier's mock is content-sensitive
        # (keyword stand-in) so the LIVE turn routes by the message text, not a
        # fixed label — the real endpoint classifies for real (§12.5).
        agent_model = build_chat_model(settings)
        # One agent PER INTENT, each bound to exactly that intent's capability set
        # (issue #31). Binding is the structural guarantee behind "only Prepare
        # Action originates a Draft": for every other intent the Draft tools are
        # not on the model's tool list at all, so there is nothing to call. The
        # default agent (full registry) remains for callers that build a
        # single-agent graph directly.
        self.agent = build_agent(agent_model, self.registry, settings)
        self.agents_by_intent: dict[str, AgentHandle] = {
            intent.value: build_agent(
                agent_model,
                self.registry,
                settings,
                bind=bind_tools_for_intent(intent, self.registry),
            )
            for intent in IntentClass
        }
        classifier_model = build_chat_model(
            settings, mock_script=_classifier_mock_script(settings, mock_intent_classifier)
        )
        self.classifier = IntentClassifier(classifier_model)
        self.turn_graph: TurnGraph = build_turn_graph(
            self.agent,
            settings,
            self.classifier,
            self.metrics,
            candidate_port=self.candidate_port,
            resolution_metrics=self.resolution_metrics,
            read_port=self.read_port,
            draft_port=self.draft_port,
            business_day=self.business_day,
            agents_by_intent=self.agents_by_intent,
        )


def _classifier_mock_script(
    settings: Settings, intent_classifier: Callable[[str], str] | None = None
) -> MockScript | None:
    """The classifier's mock script (mock provider only; ignored otherwise).

    Content-sensitive intent classification via the deterministic keyword
    stand-in, so a live mock turn routes by the actual message text. The real
    OpenAI-compatible endpoint ignores this and classifies with the model.

    ``intent_classifier`` is a TEST-ONLY stand-in lexicon. It is consulted only
    under the deterministic mock provider — the guard below returns before it is
    read on the production transport — so it can never influence a real turn. It
    exists because the default keyword lexicon only distinguishes
    Question/ApproveAction/ConfirmResult, while the cross-boundary suite must
    drive all EIGHT classes through the real ``POST /chat`` transport. Only the
    classification changes; containment, resolution, dispatch, capability binding
    and the envelope all run for real.
    """
    if settings.provider_kind is not ProviderKind.MOCK:
        return None
    return MockScript(
        mode="answer",
        response_tool_name="IntentClassification",
        intent_classifier=intent_classifier or default_keyword_intent,
    )


_BEARER_PREFIX = "Bearer "


def _extract_bearer(authorization: str | None) -> str | None:
    """The token from an ``Authorization: Bearer <token>`` header, else None.

    Anything without the exact ``Bearer `` scheme prefix (missing header, a bare
    token, ``Basic ...``) is malformed and yields ``None`` — the caller rejects.
    """
    if authorization is None or not authorization.startswith(_BEARER_PREFIX):
        return None
    token = authorization[len(_BEARER_PREFIX) :].strip()
    return token or None


def _enforce_gateway_auth(settings: Settings, authorization: str | None) -> None:
    """Reject any inbound request lacking the exact gateway bearer (issue #167).

    Constant-time comparison (``hmac.compare_digest``, never ``==``) so a wrong
    token cannot be recovered byte-by-byte from response timing. Fails closed: a
    missing / malformed / mismatched credential raises 401 and NEVER echoes the
    secret. This runs as a route dependency, so it precedes the kill-switch and
    every graph/model/registry branch — an arbitrary internal caller cannot
    reach inference or supply account/global chat controls without the bearer.
    """
    if not settings.require_gateway_auth():
        return  # explicit, documented local-test bypass (mock only)
    expected = settings.expected_gateway_token()
    if expected is None:
        # require_gateway_auth() true but nothing to compare against ⇒ fail closed.
        raise HTTPException(status_code=401, detail="gateway credential not configured")
    provided = _extract_bearer(authorization)
    if provided is None or not hmac.compare_digest(provided, expected):
        raise HTTPException(status_code=401, detail="invalid gateway credential")


def create_app(
    settings: Settings | None = None,
    *,
    http_client: httpx.Client | None = None,
    mock_intent_classifier: Callable[[str], str] | None = None,
) -> FastAPI:
    """Build the FastAPI app. Tests pass explicit settings (mock provider).

    ``http_client`` is the injection seam for the outbound gateway transport: the
    cross-boundary tests supply an ``httpx.MockTransport`` client so the adapter,
    the ports, the dispatch node and the envelope all run for real with no
    network. Production passes nothing and the app builds its own client.
    """
    resolved = settings or load_settings()
    # Fail closed at startup: the production transport must carry an inbound
    # gateway credential (issue #167) AND the outbound gateway endpoint +
    # read/Draft-only credential (issue #108, 108c). Both raise before any
    # singleton is wired — an unwired production plane never starts.
    resolved.validate_auth_config()
    resolved.validate_gateway_read_config()
    state = AppState(
        resolved, http_client=http_client, mock_intent_classifier=mock_intent_classifier
    )
    app = FastAPI(title="DK Marketplace Intelligence — LLM plane", version="0.0.0")
    app.state.app_state = state

    def require_gateway_credential(
        authorization: Annotated[str | None, Header()] = None,
    ) -> None:
        """Route dependency guarding every NON-public endpoint (issue #167)."""
        _enforce_gateway_auth(state.settings, authorization)

    # Only /healthz is public (narrow liveness probe, no secrets). Every other
    # internal endpoint requires the gateway bearer.
    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/registry/manifest", dependencies=[Depends(require_gateway_credential)])
    def registry_manifest() -> dict[str, Any]:
        return state.registry.manifest()

    @app.post("/chat", dependencies=[Depends(require_gateway_credential)])
    def chat(req: ChatRequest) -> Any:
        # Auth (issue #167) has already run as a route dependency, so this body —
        # including the kill switch below — is only reached by an authenticated
        # caller; auth precedes CHAT-009.
        # Kill switch (CHAT-009): chat-only structured disabled state. Screens —
        # and every other endpoint here — stay fully functional.
        if state.settings.chat_disabled_for(req.marketplace_account_id):
            reason = (
                "kill_switch_global"
                if state.settings.chat_disabled_global
                else "kill_switch_account"
            )
            return JSONResponse(
                status_code=503,
                content={
                    "code": "CHAT_DISABLED",
                    "message": "chat is temporarily disabled; use the structured screens",
                    "reason": reason,
                },
            )
        return StreamingResponse(
            _stream_turn(state, req),
            media_type="text/event-stream",
        )

    return app


def _turn_context_state(context: dict[str, Any] | None) -> dict[str, Any] | None:
    """Project the gateway's raw context onto JSON-safe graph state, as-of stamped.

    Graph state holds JSON-safe business data ONLY — never a pydantic instance.
    The payload is NOT validated here: ``resolve_turn_context`` owns that, so a
    malformed context fails closed structurally instead of 422-ing the turn.

    The as-of instant is stamped HERE from the server clock and ALWAYS overrides
    any client-supplied ``now``: a turn's freshness is not something a caller may
    assert. A back-dated ``now`` would move every "today/this week" boundary and
    let a historical window read as the current one (§12.3: never claim current
    state from stale evidence). Stamping once at the transport also keeps the
    resolver pure — it never reads a clock — so a turn's time resolution is
    reproducible from graph state alone.
    """
    if context is None:
        return None
    return {**context, "now": _utc_now()}


def _utc_now() -> str:
    """The turn's as-of instant (RFC 3339 UTC), read once at the boundary."""
    return datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%SZ")


def _utc_business_day() -> str:
    """The as-of business day (UTC calendar date) authoritative reads are keyed by.

    Stamped from the SERVER clock, exactly like the turn's as-of instant: a
    caller may not assert which day a briefing is "today", and the model may not
    choose one either — a back-dated day would let a historical briefing read as
    the current one (§12.3). Jalali stays a display calendar over UTC storage
    (localization boundary): no locale or calendar branch appears here.
    """
    return datetime.now(UTC).strftime("%Y-%m-%d")


async def _stream_turn(state: AppState, req: ChatRequest) -> AsyncIterator[str]:
    """Yield SSE frames for a turn: conversation, token(s), final | failure.

    Tokens are forwarded from the graph's async stream AS the model produces them
    (#23) — the async generator yields each frame directly to the ASGI transport,
    which awaits every yield, giving natural backpressure and no unbounded buffer.
    A client disconnect closes this generator, which stops the upstream stream.
    """
    conversation_id = req.conversation_id or str(uuid.uuid4())
    yield ChatStreamEvent(
        kind=StreamEventKind.CONVERSATION, conversation_id=conversation_id
    ).to_sse()

    turn_state: TurnState = {
        "message": req.message,
        # The AUTHENTICATED scope of the turn (never taken from `context`).
        "organization_id": req.organization_id,
        "marketplace_account_id": req.marketplace_account_id,
        "conversation_id": conversation_id,
        "turn_context": _turn_context_state(req.context),
    }
    async for chunk in state.turn_graph.astream_turn(turn_state):
        if chunk.kind == "token" and chunk.token is not None:
            yield ChatStreamEvent(kind=StreamEventKind.TOKEN, token=chunk.token).to_sse()
        elif chunk.kind == "final":
            yield ChatStreamEvent(kind=StreamEventKind.FINAL, envelope=chunk.answer or {}).to_sse()
        elif chunk.kind == "failure":
            yield ChatStreamEvent(kind=StreamEventKind.FAILURE, failure=chunk.failure).to_sse()
