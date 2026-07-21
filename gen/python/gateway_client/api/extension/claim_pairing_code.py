from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.pairing_claim_request import PairingClaimRequest
from ...models.pairing_credential import PairingCredential
from ...types import Response


def _get_kwargs(
    *,
    body: PairingClaimRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/ext/pairing/claim",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | PairingCredential:
    if response.status_code == 200:
        response_200 = PairingCredential.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = ErrorEnvelope.from_dict(response.json())

        return response_401

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | PairingCredential]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: PairingClaimRequest,
) -> Response[ErrorEnvelope | PairingCredential]:
    """Exchange a pairing code for a capture credential (EXT-001).

     The extension exchanges a short-lived pairing code for a scoped capture/overlay credential (PRD §14
    EXT-001). This route carries NO human session — the extension is not logged in — so it is
    authenticated only by the single-use code itself. The response holds ONLY a capture credential bound
    to a marketplace account; it never carries a seller-API token, cookie, or session. An expired,
    unknown, revoked, or already-claimed code fails closed with 401.

    Args:
        body (PairingClaimRequest): The pairing code the extension exchanges for a capture
            credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | PairingCredential]
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
    client: Client,
    body: PairingClaimRequest,
) -> ErrorEnvelope | PairingCredential | None:
    """Exchange a pairing code for a capture credential (EXT-001).

     The extension exchanges a short-lived pairing code for a scoped capture/overlay credential (PRD §14
    EXT-001). This route carries NO human session — the extension is not logged in — so it is
    authenticated only by the single-use code itself. The response holds ONLY a capture credential bound
    to a marketplace account; it never carries a seller-API token, cookie, or session. An expired,
    unknown, revoked, or already-claimed code fails closed with 401.

    Args:
        body (PairingClaimRequest): The pairing code the extension exchanges for a capture
            credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | PairingCredential
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: PairingClaimRequest,
) -> Response[ErrorEnvelope | PairingCredential]:
    """Exchange a pairing code for a capture credential (EXT-001).

     The extension exchanges a short-lived pairing code for a scoped capture/overlay credential (PRD §14
    EXT-001). This route carries NO human session — the extension is not logged in — so it is
    authenticated only by the single-use code itself. The response holds ONLY a capture credential bound
    to a marketplace account; it never carries a seller-API token, cookie, or session. An expired,
    unknown, revoked, or already-claimed code fails closed with 401.

    Args:
        body (PairingClaimRequest): The pairing code the extension exchanges for a capture
            credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | PairingCredential]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: PairingClaimRequest,
) -> ErrorEnvelope | PairingCredential | None:
    """Exchange a pairing code for a capture credential (EXT-001).

     The extension exchanges a short-lived pairing code for a scoped capture/overlay credential (PRD §14
    EXT-001). This route carries NO human session — the extension is not logged in — so it is
    authenticated only by the single-use code itself. The response holds ONLY a capture credential bound
    to a marketplace account; it never carries a seller-API token, cookie, or session. An expired,
    unknown, revoked, or already-claimed code fails closed with 401.

    Args:
        body (PairingClaimRequest): The pairing code the extension exchanges for a capture
            credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | PairingCredential
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
