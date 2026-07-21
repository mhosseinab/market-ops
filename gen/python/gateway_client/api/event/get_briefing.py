import datetime
from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.daily_briefing import DailyBriefing
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
    business_day: datetime.date,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    json_business_day = business_day.isoformat()
    params["businessDay"] = json_business_day

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/briefing",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> DailyBriefing | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = DailyBriefing.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[DailyBriefing | ErrorEnvelope]:
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
    business_day: datetime.date,
) -> Response[DailyBriefing | ErrorEnvelope]:
    """Get the stored daily briefing for a business day (CHAT-010).

     Returns the stored once-per-business-day briefing for an account (PRD §6.8, CHAT-010). The briefing
    is generated from the SAME Today ranking the feed uses, so its event ids and ORDER EQUAL the Today
    feed — a divergence is a traceability breach. This is a read; it never generates a briefing.

    Args:
        marketplace_account_id (UUID):
        business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DailyBriefing | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        business_day=business_day,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    marketplace_account_id: UUID,
    business_day: datetime.date,
) -> DailyBriefing | ErrorEnvelope | None:
    """Get the stored daily briefing for a business day (CHAT-010).

     Returns the stored once-per-business-day briefing for an account (PRD §6.8, CHAT-010). The briefing
    is generated from the SAME Today ranking the feed uses, so its event ids and ORDER EQUAL the Today
    feed — a divergence is a traceability breach. This is a read; it never generates a briefing.

    Args:
        marketplace_account_id (UUID):
        business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DailyBriefing | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        business_day=business_day,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    business_day: datetime.date,
) -> Response[DailyBriefing | ErrorEnvelope]:
    """Get the stored daily briefing for a business day (CHAT-010).

     Returns the stored once-per-business-day briefing for an account (PRD §6.8, CHAT-010). The briefing
    is generated from the SAME Today ranking the feed uses, so its event ids and ORDER EQUAL the Today
    feed — a divergence is a traceability breach. This is a read; it never generates a briefing.

    Args:
        marketplace_account_id (UUID):
        business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[DailyBriefing | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        business_day=business_day,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
    business_day: datetime.date,
) -> DailyBriefing | ErrorEnvelope | None:
    """Get the stored daily briefing for a business day (CHAT-010).

     Returns the stored once-per-business-day briefing for an account (PRD §6.8, CHAT-010). The briefing
    is generated from the SAME Today ranking the feed uses, so its event ids and ORDER EQUAL the Today
    feed — a divergence is a traceability breach. This is a read; it never generates a briefing.

    Args:
        marketplace_account_id (UUID):
        business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        DailyBriefing | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            business_day=business_day,
        )
    ).parsed
