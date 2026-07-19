"""Inbound gateway authentication for the internal LLM plane (issue #167).

Core mints a read+Draft-only bearer (``LLM_GATEWAY_TOKEN``) and presents it as
``Authorization: Bearer <token>`` on every call into this plane. These tests pin
the plane's side of that contract:

* the non-public endpoints (``POST /chat``, ``GET /registry/manifest``) reject a
  missing / malformed / wrong bearer with 401 BEFORE any graph/model/registry or
  kill-switch work runs — auth precedes CHAT-009;
* the correct bearer succeeds;
* ``GET /healthz`` stays narrowly public;
* the secret never leaks into a rejection body;
* misconfiguration fails closed at startup on the production transport, with a
  clearly-scoped mock/local-test escape hatch.

Negative cases are written first (CLAUDE.md §TDD: free-text-never-approves and
its siblings are negative-test-first invariants; so is "unauthenticated caller
never reaches inference").
"""

from __future__ import annotations

import json
from typing import Any

import pytest
from fastapi.testclient import TestClient
from llm.app import create_app
from llm.config import ProviderKind, Settings
from pydantic import SecretStr

TOKEN = "test-gateway-token-abc123"
BEARER = {"Authorization": f"Bearer {TOKEN}"}


def _mock_settings(**overrides: Any) -> Settings:
    base: dict[str, Any] = {
        "provider_kind": ProviderKind.MOCK,
        "gateway_token": SecretStr(TOKEN),
    }
    base.update(overrides)
    return Settings(**base)


def _app(**overrides: Any) -> Any:
    return create_app(_mock_settings(**overrides))


# --- NEGATIVE FIRST: no credential never reaches inference -------------------


def test_chat_without_authorization_is_rejected_and_never_streams() -> None:
    with TestClient(_app()) as client:
        resp = client.post("/chat", json={"message": "what changed today?"})
    assert resp.status_code in (401, 403)
    # Not an SSE stream — the auth error short-circuits before any graph work.
    assert not resp.headers["content-type"].startswith("text/event-stream")
    body = resp.text
    assert "conversation" not in body.lower()
    assert "data:" not in body


def test_chat_with_malformed_header_missing_bearer_prefix_is_rejected() -> None:
    with TestClient(_app()) as client:
        resp = client.post(
            "/chat",
            json={"message": "hi"},
            headers={"Authorization": TOKEN},  # no "Bearer " prefix
        )
    assert resp.status_code in (401, 403)


def test_chat_with_non_bearer_scheme_is_rejected() -> None:
    with TestClient(_app()) as client:
        resp = client.post(
            "/chat",
            json={"message": "hi"},
            headers={"Authorization": "Basic dXNlcjpwYXNz"},
        )
    assert resp.status_code in (401, 403)


def test_chat_with_wrong_bearer_is_rejected() -> None:
    with TestClient(_app()) as client:
        resp = client.post(
            "/chat",
            json={"message": "hi"},
            headers={"Authorization": "Bearer not-the-token"},
        )
    assert resp.status_code in (401, 403)


def test_chat_with_correct_bearer_succeeds_and_streams() -> None:
    with TestClient(_app()) as client:
        resp = client.post("/chat", json={"message": "what changed today?"}, headers=BEARER)
    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("text/event-stream")
    frames = [
        json.loads(block[len("data:") :].strip())
        for block in resp.text.strip().split("\n\n")
        if block.strip().startswith("data:")
    ]
    assert frames[0]["kind"] == "conversation"


def test_registry_manifest_requires_bearer() -> None:
    with TestClient(_app()) as client:
        assert client.get("/registry/manifest").status_code in (401, 403)
        assert client.get(
            "/registry/manifest", headers={"Authorization": "Bearer wrong"}
        ).status_code in (401, 403)
        ok = client.get("/registry/manifest", headers=BEARER)
    assert ok.status_code == 200
    assert set(ok.json()["kinds"]) == {"read", "draft"}


def test_healthz_stays_public() -> None:
    with TestClient(_app()) as client:
        resp = client.get("/healthz")  # no auth
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


# --- auth precedes the kill switch (CHAT-009) -------------------------------


def test_auth_runs_before_global_kill_switch() -> None:
    """A killed account with NO bearer gets the AUTH error, not the 503 state."""
    with TestClient(_app(chat_disabled_global=True)) as client:
        resp = client.post("/chat", json={"message": "hi"})  # no auth
    assert resp.status_code in (401, 403)  # not 503 — auth precedes CHAT-009


def test_auth_runs_before_account_kill_switch() -> None:
    killed = "22222222-2222-2222-2222-222222222222"
    with TestClient(_app(chat_disabled_accounts=frozenset({killed}))) as client:
        resp = client.post(
            "/chat",
            json={"message": "hi", "marketplace_account_id": killed},
            headers={"Authorization": "Bearer wrong"},
        )
    assert resp.status_code in (401, 403)  # not 503


# --- secret never leaks -----------------------------------------------------


def test_secret_never_appears_in_rejection_body() -> None:
    with TestClient(_app()) as client:
        resp = client.post(
            "/chat", json={"message": "hi"}, headers={"Authorization": "Bearer wrong"}
        )
    assert TOKEN not in resp.text
    assert TOKEN not in json.dumps(dict(resp.headers))


def test_secret_not_in_settings_repr() -> None:
    settings = _mock_settings()
    assert TOKEN not in repr(settings)
    assert TOKEN not in str(settings)


# --- config: fail closed on the production transport ------------------------


def test_production_transport_without_token_fails_closed_at_startup() -> None:
    settings = Settings(provider_kind=ProviderKind.OPENAI_COMPATIBLE, gateway_token=SecretStr(""))
    with pytest.raises((ValueError, RuntimeError)):
        create_app(settings)


def test_production_transport_with_token_starts() -> None:
    settings = Settings(
        provider_kind=ProviderKind.OPENAI_COMPATIBLE,
        gateway_token=SecretStr(TOKEN),
        # A provider credential so the OpenAI-compatible model constructs offline;
        # no call is made (registry/manifest needs no model).
        provider_api_key=SecretStr("unused-endpoint-key"),
    )
    app = create_app(settings)
    # Configured token is enforced even on the production transport.
    with TestClient(app) as client:
        assert client.get("/registry/manifest").status_code in (401, 403)
        assert client.get("/registry/manifest", headers=BEARER).status_code == 200


# --- config: the mock/local-test escape hatch -------------------------------


def test_mock_without_token_still_rejects_by_default() -> None:
    """Tokenless mock startup is allowed, but requests still fail closed."""
    app = create_app(Settings(provider_kind=ProviderKind.MOCK, gateway_token=SecretStr("")))
    with TestClient(app) as client:
        assert client.post("/chat", json={"message": "hi"}).status_code in (401, 403)
        assert client.get("/registry/manifest").status_code in (401, 403)
        assert client.get("/healthz").status_code == 200


def test_mock_local_bypass_permits_unauthenticated_requests() -> None:
    """The explicit, documented local-test bypass opens the plane (dev only)."""
    app = create_app(
        Settings(
            provider_kind=ProviderKind.MOCK,
            gateway_token=SecretStr(""),
            gateway_auth_local_bypass=True,
        )
    )
    with TestClient(app) as client:
        assert client.post("/chat", json={"message": "hi"}).status_code == 200
        assert client.get("/registry/manifest").status_code == 200


def test_require_gateway_auth_rule() -> None:
    """The fail-closed decision lives in one testable Settings method."""
    # Configured token ⇒ always enforce.
    assert _mock_settings().require_gateway_auth() is True
    assert (
        Settings(provider_kind=ProviderKind.OPENAI_COMPATIBLE, gateway_token=SecretStr(TOKEN))
        .require_gateway_auth()
        is True
    )
    # No token, no bypass ⇒ still enforce (fail closed).
    tokenless = Settings(provider_kind=ProviderKind.MOCK, gateway_token=SecretStr(""))
    assert tokenless.require_gateway_auth() is True
    # No token + explicit local bypass ⇒ do not enforce (dev only).
    assert (
        Settings(
            provider_kind=ProviderKind.MOCK,
            gateway_token=SecretStr(""),
            gateway_auth_local_bypass=True,
        ).require_gateway_auth()
        is False
    )
