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

import uuid
from collections.abc import AsyncIterator
from typing import Any

from fastapi import FastAPI
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel, ConfigDict, Field

from llm.config import ProviderKind, Settings, load_settings
from llm.envelope.models import ChatStreamEvent, StreamEventKind
from llm.intents.classifier import IntentClassifier
from llm.intents.keyword_mock import default_keyword_intent
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
        classifier_model = build_chat_model(
            settings, mock_script=_classifier_mock_script(settings)
        )
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


def create_app(settings: Settings | None = None) -> FastAPI:
    """Build the FastAPI app. Tests pass explicit settings (mock provider)."""
    resolved = settings or load_settings()
    state = AppState(resolved)
    app = FastAPI(title="DK Marketplace Intelligence — LLM plane", version="0.0.0")
    app.state.app_state = state

    @app.get("/healthz")
    def healthz() -> dict[str, str]:
        return {"status": "ok"}

    @app.get("/registry/manifest")
    def registry_manifest() -> dict[str, Any]:
        return state.registry.manifest()

    @app.post("/chat")
    def chat(req: ChatRequest) -> Any:
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
    """Yield SSE frames for a turn: conversation, token(s), final | failure."""
    conversation_id = req.conversation_id or str(uuid.uuid4())
    yield ChatStreamEvent(
        kind=StreamEventKind.CONVERSATION, conversation_id=conversation_id
    ).to_sse()

    turn_state: TurnState = {
        "message": req.message,
        "marketplace_account_id": req.marketplace_account_id,
        "conversation_id": conversation_id,
    }
    result = state.turn_graph.run(turn_state)

    if not result.ok:
        yield ChatStreamEvent(kind=StreamEventKind.FAILURE, failure=result.failure).to_sse()
        return

    answer = result.answer or {}
    summary = str(answer.get("summary", ""))
    if summary:
        yield ChatStreamEvent(kind=StreamEventKind.TOKEN, token=summary).to_sse()
    yield ChatStreamEvent(kind=StreamEventKind.FINAL, envelope=answer).to_sse()
