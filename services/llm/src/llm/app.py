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
from collections.abc import AsyncIterator
from typing import Annotated, Any

from fastapi import Depends, FastAPI, Header, HTTPException
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, ConfigDict, Field, field_validator

from llm.config import ProviderKind, Settings, load_settings
from llm.envelope.models import ChatStreamEvent, StreamEventKind
from llm.intents.classifier import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
from llm.localization import SUPPORTED_LOCALE_TAGS
from llm.metrics import ContainmentMetrics
from llm.observability import configure_observability
from llm.orchestrator.agent import build_agent
from llm.orchestrator.graph import TurnGraph, TurnState, build_turn_graph
from llm.providers.base import build_chat_model
from llm.providers.mock import MockScript
from llm.tools.registry import ToolRegistry, build_registry


class ChatRequest(BaseModel):
    """A conversation turn from the gateway. Free text carries no authority."""

    model_config = ConfigDict(extra="ignore")

    message: str = Field(min_length=1, max_length=8000)
    conversation_id: str | None = None
    marketplace_account_id: str | None = None
    user_id: str | None = None
    organization_id: str | None = None
    # The server-authoritative bound locale for this turn (issue #120). The
    # gateway validates + binds it and sends it on every turn; the plane
    # re-validates against the SAME closed set and FAILS CLOSED (422) on an
    # unsupported tag — never inferred from the message text, digit shape, region,
    # or account. Absent (``None``) maps only via the settings fallback policy
    # (LOC-004) at compose time.
    locale: str | None = None

    @field_validator("locale")
    @classmethod
    def _validate_locale(cls, v: str | None) -> str | None:
        if v is None or v in SUPPORTED_LOCALE_TAGS:
            return v
        raise ValueError(
            f"locale {v!r} is not in the supported set {sorted(SUPPORTED_LOCALE_TAGS)} "
            "(LOC-001, issue #120 — fail closed, never inferred)"
        )


class AppState:
    """Process-wide singletons wired once at startup."""

    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.observability = configure_observability(settings)
        self.registry: ToolRegistry = build_registry()
        self.metrics = ContainmentMetrics()
        # The agent model (answers) and the classifier model are separate roles.
        # In production both resolve to the SAME configured OpenAI-compatible
        # endpoint; with the mock they carry different deterministic scripts so a
        # turn can classify AND answer. The classifier's mock is content-sensitive
        # (keyword stand-in) so the LIVE turn routes by the message text, not a
        # fixed label — the real endpoint classifies for real (§12.5).
        agent_model = build_chat_model(settings)
        self.agent = build_agent(agent_model, self.registry, settings)
        classifier_model = build_chat_model(settings, mock_script=_classifier_mock_script(settings))
        self.classifier = IntentClassifier(classifier_model)
        self.turn_graph: TurnGraph = build_turn_graph(
            self.agent, settings, self.classifier, self.metrics
        )


def _classifier_mock_script(settings: Settings) -> MockScript | None:
    """The classifier's mock script (mock provider only; ignored otherwise).

    Content-sensitive intent classification via the deterministic keyword
    stand-in, so a live mock turn routes by the actual message text. The real
    OpenAI-compatible endpoint ignores this and classifies with the model.
    """
    if settings.provider_kind is not ProviderKind.MOCK:
        return None
    return MockScript(
        mode="answer",
        response_tool_name="IntentClassification",
        intent_classifier=default_keyword_intent,
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


def create_app(settings: Settings | None = None) -> FastAPI:
    """Build the FastAPI app. Tests pass explicit settings (mock provider)."""
    resolved = settings or load_settings()
    # Fail closed at startup: the production transport must carry an inbound
    # gateway credential (issue #167). Raises before any singletons are wired.
    resolved.validate_auth_config()
    state = AppState(resolved)
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


async def _stream_turn(state: AppState, req: ChatRequest) -> AsyncIterator[str]:
    """Yield SSE frames for a turn: conversation, token(s), final | failure.

    Tokens are forwarded from the graph's async stream AS the model produces them
    (#23) — the async generator yields each frame directly to the ASGI transport,
    which awaits every yield, giving natural backpressure and no unbounded buffer.
    A client disconnect closes this generator, which stops the upstream stream.
    """
    conversation_id = req.conversation_id or str(uuid.uuid4())
    # Resolve the server-authoritative bound locale (issue #120). A present tag was
    # already validated on the request (fail closed, 422); a missing one maps via
    # the explicit fallback policy (LOC-004). The plane echoes it on its own
    # `conversation` frame so the bound locale travels with the turn even before
    # the gateway rewrites the frame with its authoritative context echo.
    locale_tag = state.settings.resolve_turn_locale(req.locale)
    yield ChatStreamEvent(
        kind=StreamEventKind.CONVERSATION,
        conversation_id=conversation_id,
        locale_tag=locale_tag,
    ).to_sse()

    turn_state: TurnState = {
        "message": req.message,
        "marketplace_account_id": req.marketplace_account_id,
        "conversation_id": conversation_id,
    }
    async for chunk in state.turn_graph.astream_turn(turn_state):
        if chunk.kind == "token" and chunk.token is not None:
            yield ChatStreamEvent(kind=StreamEventKind.TOKEN, token=chunk.token).to_sse()
        elif chunk.kind == "final":
            yield ChatStreamEvent(kind=StreamEventKind.FINAL, envelope=chunk.answer or {}).to_sse()
        elif chunk.kind == "failure":
            yield ChatStreamEvent(kind=StreamEventKind.FAILURE, failure=chunk.failure).to_sse()
