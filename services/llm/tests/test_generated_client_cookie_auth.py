from typing import get_args, get_type_hints
from uuid import UUID

import httpx
from gateway_client import AuthenticatedClient, Client
from gateway_client.api.cost import get_cost_import_preview, get_margin_readiness
from gateway_client.api.observation import upload_capture
from gateway_client.api.organization import connect_connector, get_connector_status
from gateway_client.api.recommendation import create_recommendation_draft
from gateway_client.api.session import login, logout
from gateway_client.api.system import get_healthz
from gateway_client.models.connector_connect_request import ConnectorConnectRequest
from gateway_client.models.login_request import LoginRequest


def test_connector_generated_client_requires_cookie_capable_client() -> None:
    write_hints = get_type_hints(connect_connector.sync_detailed)
    status_hints = get_type_hints(get_connector_status.sync_detailed)

    assert write_hints["client"] is Client
    assert set(get_args(status_hints["client"])) == {AuthenticatedClient, Client}


def test_generated_client_types_match_each_contract_auth_mode() -> None:
    assert get_type_hints(get_healthz.sync_detailed)["client"] is Client
    assert (
        get_type_hints(create_recommendation_draft.sync_detailed)["client"] is AuthenticatedClient
    )
    assert set(get_args(get_type_hints(upload_capture.sync_detailed)["client"])) == {
        AuthenticatedClient,
        Client,
    }
    assert set(get_args(get_type_hints(get_margin_readiness.sync_detailed)["client"])) == {
        AuthenticatedClient,
        Client,
    }
    assert get_type_hints(get_cost_import_preview.sync_detailed)["client"] is Client
    assert get_type_hints(logout.sync_detailed)["client"] is Client


def test_login_cookie_is_reused_for_connector_call_without_bearer() -> None:
    requests: list[httpx.Request] = []
    marketplace_account_id = UUID("00000000-0000-0000-0000-000000000003")

    def handle(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.url.path == "/auth/login":
            return httpx.Response(
                200,
                headers={"set-cookie": "mo_session=session-token; Path=/; HttpOnly"},
                json={
                    "userId": "00000000-0000-0000-0000-000000000001",
                    "organizationId": "00000000-0000-0000-0000-000000000002",
                    "email": "owner@example.test",
                    "role": "owner",
                    "expiresAt": "2030-01-01T00:00:00Z",
                },
            )
        if request.url.path == "/connector/connect":
            assert request.headers.get("authorization") is None
            assert request.headers.get("cookie") == "mo_session=session-token"
            return httpx.Response(
                200,
                json={
                    "marketplaceAccountId": "00000000-0000-0000-0000-000000000003",
                    "connectionState": "connected",
                    "capabilities": [],
                },
            )
        raise AssertionError(f"unexpected request: {request.method} {request.url}")

    client = Client(base_url="https://gateway.test")
    client.set_httpx_client(
        httpx.Client(base_url="https://gateway.test", transport=httpx.MockTransport(handle))
    )

    login.sync_detailed(
        client=client,
        body=LoginRequest(email="owner@example.test", password="correct horse battery staple"),
    )
    connect_connector.sync_detailed(
        client=client,
        body=ConnectorConnectRequest(
            marketplace_account_id=marketplace_account_id,
            authorization_code="authorization-code",
        ),
    )

    assert [request.url.path for request in requests] == ["/auth/login", "/connector/connect"]
