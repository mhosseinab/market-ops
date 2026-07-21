from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.watchlist_add_request import WatchlistAddRequest
from ...models.watchlist_entry import WatchlistEntry
from ...types import Response


def _get_kwargs(
    *,
    body: WatchlistAddRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/watchlist",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | WatchlistEntry:
    if response.status_code == 200:
        response_200 = WatchlistEntry.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | WatchlistEntry]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: WatchlistAddRequest,
) -> Response[ErrorEnvelope | WatchlistEntry]:
    r"""Add a Confirmed owned product to the priority watchlist (EXT-007).

     Adds one Confirmed owned product (CAT-002 Confirmed Market Product Identity) to the account's
    priority watchlist. The SERVER enforces the watchlist cap (PRD EXT-007 \"Server enforces cap\") — a
    request that would exceed it is rejected, never silently truncated. Every accepted add appends an
    append-only AUD-001 audit record ATOMICALLY with the insert, on the SAME transaction (EXT-007
    \"change is audited\"). L2 config.watchlist, Owner + Operator only.

    Args:
        body (WatchlistAddRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | WatchlistEntry]
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
    body: WatchlistAddRequest,
) -> ErrorEnvelope | WatchlistEntry | None:
    r"""Add a Confirmed owned product to the priority watchlist (EXT-007).

     Adds one Confirmed owned product (CAT-002 Confirmed Market Product Identity) to the account's
    priority watchlist. The SERVER enforces the watchlist cap (PRD EXT-007 \"Server enforces cap\") — a
    request that would exceed it is rejected, never silently truncated. Every accepted add appends an
    append-only AUD-001 audit record ATOMICALLY with the insert, on the SAME transaction (EXT-007
    \"change is audited\"). L2 config.watchlist, Owner + Operator only.

    Args:
        body (WatchlistAddRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | WatchlistEntry
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: WatchlistAddRequest,
) -> Response[ErrorEnvelope | WatchlistEntry]:
    r"""Add a Confirmed owned product to the priority watchlist (EXT-007).

     Adds one Confirmed owned product (CAT-002 Confirmed Market Product Identity) to the account's
    priority watchlist. The SERVER enforces the watchlist cap (PRD EXT-007 \"Server enforces cap\") — a
    request that would exceed it is rejected, never silently truncated. Every accepted add appends an
    append-only AUD-001 audit record ATOMICALLY with the insert, on the SAME transaction (EXT-007
    \"change is audited\"). L2 config.watchlist, Owner + Operator only.

    Args:
        body (WatchlistAddRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | WatchlistEntry]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: WatchlistAddRequest,
) -> ErrorEnvelope | WatchlistEntry | None:
    r"""Add a Confirmed owned product to the priority watchlist (EXT-007).

     Adds one Confirmed owned product (CAT-002 Confirmed Market Product Identity) to the account's
    priority watchlist. The SERVER enforces the watchlist cap (PRD EXT-007 \"Server enforces cap\") — a
    request that would exceed it is rejected, never silently truncated. Every accepted add appends an
    append-only AUD-001 audit record ATOMICALLY with the insert, on the SAME transaction (EXT-007
    \"change is audited\"). L2 config.watchlist, Owner + Operator only.

    Args:
        body (WatchlistAddRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | WatchlistEntry
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
