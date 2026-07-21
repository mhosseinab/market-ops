from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.pairing_code import PairingCode
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/ext/pairing/code",
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | PairingCode:
    if response.status_code == 201:
        response_201 = PairingCode.from_dict(response.json())

        return response_201

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | PairingCode]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
) -> Response[ErrorEnvelope | PairingCode]:
    """Mint a short-lived extension pairing code (EXT-001).

     A logged-in human mints a short-lived, single-use pairing code for the browser extension (PRD §14
    EXT-001). The code is displayed to the user and typed into the extension, which exchanges it via
    /ext/pairing/claim for a capture/overlay credential. The extension NEVER receives a seller-API
    token: the pairing flow only ever yields a scoped capture credential. The code is bound to the
    caller's marketplace account and expires quickly.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | PairingCode]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
) -> ErrorEnvelope | PairingCode | None:
    """Mint a short-lived extension pairing code (EXT-001).

     A logged-in human mints a short-lived, single-use pairing code for the browser extension (PRD §14
    EXT-001). The code is displayed to the user and typed into the extension, which exchanges it via
    /ext/pairing/claim for a capture/overlay credential. The extension NEVER receives a seller-API
    token: the pairing flow only ever yields a scoped capture credential. The code is bound to the
    caller's marketplace account and expires quickly.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | PairingCode
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
) -> Response[ErrorEnvelope | PairingCode]:
    """Mint a short-lived extension pairing code (EXT-001).

     A logged-in human mints a short-lived, single-use pairing code for the browser extension (PRD §14
    EXT-001). The code is displayed to the user and typed into the extension, which exchanges it via
    /ext/pairing/claim for a capture/overlay credential. The extension NEVER receives a seller-API
    token: the pairing flow only ever yields a scoped capture credential. The code is bound to the
    caller's marketplace account and expires quickly.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | PairingCode]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
) -> ErrorEnvelope | PairingCode | None:
    """Mint a short-lived extension pairing code (EXT-001).

     A logged-in human mints a short-lived, single-use pairing code for the browser extension (PRD §14
    EXT-001). The code is displayed to the user and typed into the extension, which exchanges it via
    /ext/pairing/claim for a capture/overlay credential. The extension NEVER receives a seller-API
    token: the pairing flow only ever yields a scoped capture credential. The code is bound to the
    caller's marketplace account and expires quickly.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | PairingCode
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
