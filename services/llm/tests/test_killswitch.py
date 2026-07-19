"""Kill-switch tests (CHAT-009): /chat disabled while read endpoints stay 200."""

from __future__ import annotations

from typing import Any

from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings
from pydantic import SecretStr

# Since issue #167 the non-public endpoints require the inbound gateway bearer.
_GATEWAY_TOKEN = "test-gateway-token"
AUTH_HEADERS = {"Authorization": f"Bearer {_GATEWAY_TOKEN}"}


def mock_settings(**overrides: Any) -> Settings:
    base: dict[str, Any] = {
        "provider_kind": ProviderKind.MOCK,
        "gateway_token": SecretStr(_GATEWAY_TOKEN),
    }
    base.update(overrides)
    return Settings(**base)


def test_global_kill_switch_disables_chat_but_not_screens() -> None:
    settings = mock_settings(chat_disabled_global=True)
    app = create_app(settings)
    with TestClient(app) as client:
        chat = client.post("/chat", json={"message": "hello"}, headers=AUTH_HEADERS)
        assert chat.status_code == 503
        body = chat.json()
        assert body["reason"] == "kill_switch_global"

        # Sampled read endpoints stay fully functional — nothing else degrades.
        assert client.get("/registry/manifest", headers=AUTH_HEADERS).status_code == 200
        assert client.get("/healthz").status_code == 200


def test_per_account_kill_switch() -> None:
    killed = "22222222-2222-2222-2222-222222222222"
    other = "33333333-3333-3333-3333-333333333333"
    settings = mock_settings(chat_disabled_accounts=frozenset({killed}))
    app = create_app(settings)
    with TestClient(app) as client:
        blocked = client.post(
            "/chat",
            json={"message": "x", "marketplace_account_id": killed},
            headers=AUTH_HEADERS,
        )
        assert blocked.status_code == 503
        assert blocked.json()["reason"] == "kill_switch_account"

        allowed = client.post(
            "/chat",
            json={"message": "x", "marketplace_account_id": other},
            headers=AUTH_HEADERS,
        )
        assert allowed.status_code == 200
        assert allowed.headers["content-type"].startswith("text/event-stream")


def test_chat_enabled_by_default() -> None:
    app = create_app(mock_settings())
    with TestClient(app) as client:
        assert (
            client.post("/chat", json={"message": "hi"}, headers=AUTH_HEADERS).status_code == 200
        )
