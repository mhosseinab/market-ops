from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.connector_connect_request import ConnectorConnectRequest
from ...models.connector_status import ConnectorStatus
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: ConnectorConnectRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/connector/connect",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ConnectorStatus | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = ConnectorStatus.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ConnectorStatus | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ConnectorConnectRequest,
) -> Response[ConnectorStatus | ErrorEnvelope]:
    """Connect the DK account for a marketplace account.

     Exchanges a DK authorization code for tokens (stored encrypted at rest), seeds every §15.2
    capability at Unknown, and runs the capability probes. Returns the reconciled connection +
    capability status (ACC-001).

    Args:
        body (ConnectorConnectRequest): Connect request. The authorization code is the value DK
            redirects back after the seller approves access (Seller Academy token guide, §0.1). It is
            exchanged server-side for tokens and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConnectorStatus | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: ConnectorConnectRequest,
) -> ConnectorStatus | ErrorEnvelope | None:
    """Connect the DK account for a marketplace account.

     Exchanges a DK authorization code for tokens (stored encrypted at rest), seeds every §15.2
    capability at Unknown, and runs the capability probes. Returns the reconciled connection +
    capability status (ACC-001).

    Args:
        body (ConnectorConnectRequest): Connect request. The authorization code is the value DK
            redirects back after the seller approves access (Seller Academy token guide, §0.1). It is
            exchanged server-side for tokens and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConnectorStatus | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ConnectorConnectRequest,
) -> Response[ConnectorStatus | ErrorEnvelope]:
    """Connect the DK account for a marketplace account.

     Exchanges a DK authorization code for tokens (stored encrypted at rest), seeds every §15.2
    capability at Unknown, and runs the capability probes. Returns the reconciled connection +
    capability status (ACC-001).

    Args:
        body (ConnectorConnectRequest): Connect request. The authorization code is the value DK
            redirects back after the seller approves access (Seller Academy token guide, §0.1). It is
            exchanged server-side for tokens and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ConnectorStatus | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ConnectorConnectRequest,
) -> ConnectorStatus | ErrorEnvelope | None:
    """Connect the DK account for a marketplace account.

     Exchanges a DK authorization code for tokens (stored encrypted at rest), seeds every §15.2
    capability at Unknown, and runs the capability probes. Returns the reconciled connection +
    capability status (ACC-001).

    Args:
        body (ConnectorConnectRequest): Connect request. The authorization code is the value DK
            redirects back after the seller approves access (Seller Academy token guide, §0.1). It is
            exchanged server-side for tokens and never persisted in plaintext.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ConnectorStatus | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
