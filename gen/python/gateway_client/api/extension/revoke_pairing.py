from http import HTTPStatus
from typing import Any, cast

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/ext/pairing/revoke",
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> Any | ErrorEnvelope:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[Any | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
) -> Response[Any | ErrorEnvelope]:
    """Revoke a paired extension's capture credential (EXT-001).

     A logged-in human revokes the capture credential(s) for their marketplace account (PRD §14
    EXT-001/EXT-009 kill switch). After revocation the extension's next capture upload fails closed with
    401 and the popup shows a visibly disabled state — never a silent no-op. Idempotent: revoking an
    already-revoked or absent credential succeeds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ErrorEnvelope]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
) -> Any | ErrorEnvelope | None:
    """Revoke a paired extension's capture credential (EXT-001).

     A logged-in human revokes the capture credential(s) for their marketplace account (PRD §14
    EXT-001/EXT-009 kill switch). After revocation the extension's next capture upload fails closed with
    401 and the popup shows a visibly disabled state — never a silent no-op. Idempotent: revoking an
    already-revoked or absent credential succeeds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
) -> Response[Any | ErrorEnvelope]:
    """Revoke a paired extension's capture credential (EXT-001).

     A logged-in human revokes the capture credential(s) for their marketplace account (PRD §14
    EXT-001/EXT-009 kill switch). After revocation the extension's next capture upload fails closed with
    401 and the popup shows a visibly disabled state — never a silent no-op. Idempotent: revoking an
    already-revoked or absent credential succeeds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ErrorEnvelope]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
) -> Any | ErrorEnvelope | None:
    """Revoke a paired extension's capture credential (EXT-001).

     A logged-in human revokes the capture credential(s) for their marketplace account (PRD §14
    EXT-001/EXT-009 kill switch). After revocation the extension's next capture upload fails closed with
    401 and the popup shows a visibly disabled state — never a silent no-op. Idempotent: revoking an
    already-revoked or absent credential succeeds.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
