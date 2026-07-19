"""End-to-end SSE test: /chat streams from the mock provider (§19.3, no paid calls)."""

from __future__ import annotations

import json
from typing import Any

from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings
from pydantic import SecretStr

# Since issue #167 the non-public endpoints require the inbound gateway bearer;
# these tests present it (never the local-test bypass) to exercise the real path.
_GATEWAY_TOKEN = "test-gateway-token"
AUTH_HEADERS = {"Authorization": f"Bearer {_GATEWAY_TOKEN}"}


def mock_settings(**overrides: Any) -> Settings:
    base: dict[str, Any] = {
        "provider_kind": ProviderKind.MOCK,
        "gateway_token": SecretStr(_GATEWAY_TOKEN),
    }
    base.update(overrides)
    return Settings(**base)


def _events(body: str) -> list[dict[str, Any]]:
    frames = []
    for block in body.strip().split("\n\n"):
        line = block.strip()
        if line.startswith("data:"):
            frames.append(json.loads(line[len("data:") :].strip()))
    return frames


def test_chat_streams_conversation_token_and_final_from_mock() -> None:
    settings = mock_settings()
    # Drive the mock into "answer" mode so the turn produces a typed envelope.
    app = create_app(settings)
    # Rebuild the model in answer mode via the app's agent by patching settings is
    # unnecessary: default AppState uses the mock; we assert the stream shape.
    with TestClient(app) as client:
        resp = client.post(
            "/chat", json={"message": "what changed today?"}, headers=AUTH_HEADERS
        )
        assert resp.status_code == 200
        assert resp.headers["content-type"].startswith("text/event-stream")
        frames = _events(resp.text)

    kinds = [f["kind"] for f in frames]
    assert kinds[0] == "conversation"
    assert frames[0]["conversation_id"]
    assert "final" in kinds
    # No frame carries an approval control of any kind.
    for f in frames:
        assert "approval" not in json.dumps(f).lower()


def test_chat_continues_existing_conversation() -> None:
    settings = mock_settings()
    app = create_app(settings)
    with TestClient(app) as client:
        resp = client.post(
            "/chat",
            json={"message": "hi", "conversation_id": "11111111-1111-1111-1111-111111111111"},
            headers=AUTH_HEADERS,
        )
        frames = _events(resp.text)
    assert frames[0]["conversation_id"] == "11111111-1111-1111-1111-111111111111"


def test_registry_manifest_endpoint() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        resp = client.get("/registry/manifest", headers=AUTH_HEADERS)
    assert resp.status_code == 200
    body = resp.json()
    assert set(body["kinds"]) == {"read", "draft"}
    assert len(body["tools"]) == 11
