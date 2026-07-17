"""Kill-switch tests (CHAT-009): /chat disabled while read endpoints stay 200."""

from __future__ import annotations

from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings


def test_global_kill_switch_disables_chat_but_not_screens() -> None:
    settings = Settings(provider_kind=ProviderKind.MOCK, chat_disabled_global=True)
    app = create_app(settings)
    with TestClient(app) as client:
        chat = client.post("/chat", json={"message": "hello"})
        assert chat.status_code == 503
        body = chat.json()
        assert body["reason"] == "kill_switch_global"

        # Sampled read endpoints stay fully functional — nothing else degrades.
        assert client.get("/registry/manifest").status_code == 200
        assert client.get("/healthz").status_code == 200


def test_per_account_kill_switch() -> None:
    killed = "22222222-2222-2222-2222-222222222222"
    other = "33333333-3333-3333-3333-333333333333"
    settings = Settings(provider_kind=ProviderKind.MOCK, chat_disabled_accounts=frozenset({killed}))
    app = create_app(settings)
    with TestClient(app) as client:
        blocked = client.post("/chat", json={"message": "x", "marketplace_account_id": killed})
        assert blocked.status_code == 503
        assert blocked.json()["reason"] == "kill_switch_account"

        allowed = client.post("/chat", json={"message": "x", "marketplace_account_id": other})
        assert allowed.status_code == 200
        assert allowed.headers["content-type"].startswith("text/event-stream")


def test_chat_enabled_by_default() -> None:
    app = create_app(Settings(provider_kind=ProviderKind.MOCK))
    with TestClient(app) as client:
        assert client.post("/chat", json={"message": "hi"}).status_code == 200
