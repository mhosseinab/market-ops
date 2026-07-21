import datetime
from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.latest_briefing_read import LatestBriefingRead
from ...types import UNSET, Response


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
    before_business_day: datetime.date,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    json_before_business_day = before_business_day.isoformat()
    params["beforeBusinessDay"] = json_before_business_day

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/briefing/latest",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | LatestBriefingRead:
    if response.status_code == 200:
        response_200 = LatestBriefingRead.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | LatestBriefingRead]:
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
    before_business_day: datetime.date,
) -> Response[ErrorEnvelope | LatestBriefingRead]:
    """Get the latest stored briefing before a business day.

     Returns authoritative provenance for the latest stored briefing strictly before `beforeBusinessDay`.
    This read never generates a briefing and never substitutes the requested date. `available` always
    carries a stored `DailyBriefing`; `never_generated` means no earlier stored briefing exists. Service
    or storage failures use the error envelope and are not collapsed into `never_generated`.

    Args:
        marketplace_account_id (UUID):
        before_business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | LatestBriefingRead]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        before_business_day=before_business_day,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    marketplace_account_id: UUID,
    before_business_day: datetime.date,
) -> ErrorEnvelope | LatestBriefingRead | None:
    """Get the latest stored briefing before a business day.

     Returns authoritative provenance for the latest stored briefing strictly before `beforeBusinessDay`.
    This read never generates a briefing and never substitutes the requested date. `available` always
    carries a stored `DailyBriefing`; `never_generated` means no earlier stored briefing exists. Service
    or storage failures use the error envelope and are not collapsed into `never_generated`.

    Args:
        marketplace_account_id (UUID):
        before_business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | LatestBriefingRead
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        before_business_day=before_business_day,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    before_business_day: datetime.date,
) -> Response[ErrorEnvelope | LatestBriefingRead]:
    """Get the latest stored briefing before a business day.

     Returns authoritative provenance for the latest stored briefing strictly before `beforeBusinessDay`.
    This read never generates a briefing and never substitutes the requested date. `available` always
    carries a stored `DailyBriefing`; `never_generated` means no earlier stored briefing exists. Service
    or storage failures use the error envelope and are not collapsed into `never_generated`.

    Args:
        marketplace_account_id (UUID):
        before_business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | LatestBriefingRead]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        before_business_day=before_business_day,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
    before_business_day: datetime.date,
) -> ErrorEnvelope | LatestBriefingRead | None:
    """Get the latest stored briefing before a business day.

     Returns authoritative provenance for the latest stored briefing strictly before `beforeBusinessDay`.
    This read never generates a briefing and never substitutes the requested date. `available` always
    carries a stored `DailyBriefing`; `never_generated` means no earlier stored briefing exists. Service
    or storage failures use the error envelope and are not collapsed into `never_generated`.

    Args:
        marketplace_account_id (UUID):
        before_business_day (datetime.date):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | LatestBriefingRead
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            before_business_day=before_business_day,
        )
    ).parsed
