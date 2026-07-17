from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.market_event_list import MarketEventList
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
        "url": "/events",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | MarketEventList:
    if response.status_code == 200:
        response_200 = MarketEventList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | MarketEventList]:
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
) -> Response[ErrorEnvelope | MarketEventList]:
    """List the account's open market events.

     Returns the account's open|updated market events (PRD §7.4 EVT-001), the §15.1 lifecycle records.
    Each event cites its observation evidence with the observed quality state as-is (never upgraded) and
    carries its versioned materiality threshold provenance (EVT-002). Exposure is either a known Money
    amount or explicitly unknown — a missing sales/cost context is never a fabricated number (EVT-005).
    Ordering here is the stable base order; the deterministic exposure×confidence×urgency rank is on
    /today.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarketEventList]
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
) -> ErrorEnvelope | MarketEventList | None:
    """List the account's open market events.

     Returns the account's open|updated market events (PRD §7.4 EVT-001), the §15.1 lifecycle records.
    Each event cites its observation evidence with the observed quality state as-is (never upgraded) and
    carries its versioned materiality threshold provenance (EVT-002). Exposure is either a known Money
    amount or explicitly unknown — a missing sales/cost context is never a fabricated number (EVT-005).
    Ordering here is the stable base order; the deterministic exposure×confidence×urgency rank is on
    /today.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarketEventList
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | MarketEventList]:
    """List the account's open market events.

     Returns the account's open|updated market events (PRD §7.4 EVT-001), the §15.1 lifecycle records.
    Each event cites its observation evidence with the observed quality state as-is (never upgraded) and
    carries its versioned materiality threshold provenance (EVT-002). Exposure is either a known Money
    amount or explicitly unknown — a missing sales/cost context is never a fabricated number (EVT-005).
    Ordering here is the stable base order; the deterministic exposure×confidence×urgency rank is on
    /today.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarketEventList]
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
) -> ErrorEnvelope | MarketEventList | None:
    """List the account's open market events.

     Returns the account's open|updated market events (PRD §7.4 EVT-001), the §15.1 lifecycle records.
    Each event cites its observation evidence with the observed quality state as-is (never upgraded) and
    carries its versioned materiality threshold provenance (EVT-002). Exposure is either a known Money
    amount or explicitly unknown — a missing sales/cost context is never a fabricated number (EVT-005).
    Ordering here is the stable base order; the deterministic exposure×confidence×urgency rank is on
    /today.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarketEventList
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
