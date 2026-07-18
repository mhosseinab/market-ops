from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.notification_feed import NotificationFeed
from ...types import UNSET, Response


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/notifications",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | NotificationFeed:
    if response.status_code == 200:
        response_200 = NotificationFeed.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | NotificationFeed]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | NotificationFeed]:
    """List the in-app notifications for an account (NOT-001).

     Returns the account's in-app notification feed, newest first (PRD §7.5 NOT-001). Each item carries
    the SHARED product event id — the same id the daily email digest references, so a notification is
    one event on two surfaces, never two. Items reference locale catalog KEYS with named slots
    (LOC-002); the surface renders copy, the core stores none. This is a read.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | NotificationFeed]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | NotificationFeed | None:
    """List the in-app notifications for an account (NOT-001).

     Returns the account's in-app notification feed, newest first (PRD §7.5 NOT-001). Each item carries
    the SHARED product event id — the same id the daily email digest references, so a notification is
    one event on two surfaces, never two. Items reference locale catalog KEYS with named slots
    (LOC-002); the surface renders copy, the core stores none. This is a read.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | NotificationFeed
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | NotificationFeed]:
    """List the in-app notifications for an account (NOT-001).

     Returns the account's in-app notification feed, newest first (PRD §7.5 NOT-001). Each item carries
    the SHARED product event id — the same id the daily email digest references, so a notification is
    one event on two surfaces, never two. Items reference locale catalog KEYS with named slots
    (LOC-002); the surface renders copy, the core stores none. This is a read.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | NotificationFeed]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | NotificationFeed | None:
    """List the in-app notifications for an account (NOT-001).

     Returns the account's in-app notification feed, newest first (PRD §7.5 NOT-001). Each item carries
    the SHARED product event id — the same id the daily email digest references, so a notification is
    one event on two surfaces, never two. Items reference locale catalog KEYS with named slots
    (LOC-002); the surface renders copy, the core stores none. This is a read.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | NotificationFeed
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
