from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.today_feed import TodayFeed
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
        "url": "/today",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | TodayFeed:
    if response.status_code == 200:
        response_200 = TodayFeed.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | TodayFeed]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | TodayFeed]:
    """Get the ranked Today feed for the account.

     Returns the account's open events ranked for the Today screen (PRD §7.4 EVT-004): ordering is
    exposure × confidence × urgency with a DETERMINISTIC final rank and a stable tie-break. All THREE
    ranking factors are exposed on every item. Known-exposure events rank ahead of unknown-exposure
    ones; an unknown exposure is never coerced into a number (EVT-005).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | TodayFeed]
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
    client: Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | TodayFeed | None:
    """Get the ranked Today feed for the account.

     Returns the account's open events ranked for the Today screen (PRD §7.4 EVT-004): ordering is
    exposure × confidence × urgency with a DETERMINISTIC final rank and a stable tie-break. All THREE
    ranking factors are exposed on every item. Known-exposure events rank ahead of unknown-exposure
    ones; an unknown exposure is never coerced into a number (EVT-005).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | TodayFeed
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | TodayFeed]:
    """Get the ranked Today feed for the account.

     Returns the account's open events ranked for the Today screen (PRD §7.4 EVT-004): ordering is
    exposure × confidence × urgency with a DETERMINISTIC final rank and a stable tie-break. All THREE
    ranking factors are exposed on every item. Known-exposure events rank ahead of unknown-exposure
    ones; an unknown exposure is never coerced into a number (EVT-005).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | TodayFeed]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | TodayFeed | None:
    """Get the ranked Today feed for the account.

     Returns the account's open events ranked for the Today screen (PRD §7.4 EVT-004): ordering is
    exposure × confidence × urgency with a DETERMINISTIC final rank and a stable tie-break. All THREE
    ranking factors are exposed on every item. Known-exposure events rank ahead of unknown-exposure
    ones; an unknown exposure is never coerced into a number (EVT-005).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | TodayFeed
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
