from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.observed_offer_list import ObservedOfferList
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
        "url": "/market/conflicts",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | ObservedOfferList:
    if response.status_code == 200:
        response_200 = ObservedOfferList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | ObservedOfferList]:
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
) -> Response[ErrorEnvelope | ObservedOfferList]:
    r"""List cross-route conflicted Observed Offers (Market conflict banner, PD-3 item 8).

     Returns the account's Observed Offers currently in the `conflicted` quality state (§16 \"routes
    disagree → Conflicted; block\") — the values the Market screen's conflict banner surfaces. The
    underlying price of record is left intact (never zeroed, never silently overwritten); only the
    quality state blocks recommend/execute (§10.3 matrix). This is a read, same L1 read.observations
    posture as the other observation reads. Each returned offer carries `conflictEvidence` (issue #94):
    the per-route disagreeing values/units, availability, and capture/freshness times so the operator
    can inspect WHY the offer is blocked, or an explicit `unavailable` state when that comparison
    evidence can no longer be inspected.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ObservedOfferList]
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
) -> ErrorEnvelope | ObservedOfferList | None:
    r"""List cross-route conflicted Observed Offers (Market conflict banner, PD-3 item 8).

     Returns the account's Observed Offers currently in the `conflicted` quality state (§16 \"routes
    disagree → Conflicted; block\") — the values the Market screen's conflict banner surfaces. The
    underlying price of record is left intact (never zeroed, never silently overwritten); only the
    quality state blocks recommend/execute (§10.3 matrix). This is a read, same L1 read.observations
    posture as the other observation reads. Each returned offer carries `conflictEvidence` (issue #94):
    the per-route disagreeing values/units, availability, and capture/freshness times so the operator
    can inspect WHY the offer is blocked, or an explicit `unavailable` state when that comparison
    evidence can no longer be inspected.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ObservedOfferList
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | ObservedOfferList]:
    r"""List cross-route conflicted Observed Offers (Market conflict banner, PD-3 item 8).

     Returns the account's Observed Offers currently in the `conflicted` quality state (§16 \"routes
    disagree → Conflicted; block\") — the values the Market screen's conflict banner surfaces. The
    underlying price of record is left intact (never zeroed, never silently overwritten); only the
    quality state blocks recommend/execute (§10.3 matrix). This is a read, same L1 read.observations
    posture as the other observation reads. Each returned offer carries `conflictEvidence` (issue #94):
    the per-route disagreeing values/units, availability, and capture/freshness times so the operator
    can inspect WHY the offer is blocked, or an explicit `unavailable` state when that comparison
    evidence can no longer be inspected.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ObservedOfferList]
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
) -> ErrorEnvelope | ObservedOfferList | None:
    r"""List cross-route conflicted Observed Offers (Market conflict banner, PD-3 item 8).

     Returns the account's Observed Offers currently in the `conflicted` quality state (§16 \"routes
    disagree → Conflicted; block\") — the values the Market screen's conflict banner surfaces. The
    underlying price of record is left intact (never zeroed, never silently overwritten); only the
    quality state blocks recommend/execute (§10.3 matrix). This is a read, same L1 read.observations
    posture as the other observation reads. Each returned offer carries `conflictEvidence` (issue #94):
    the per-route disagreeing values/units, availability, and capture/freshness times so the operator
    can inspect WHY the offer is blocked, or an explicit `unavailable` state when that comparison
    evidence can no longer be inspected.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ObservedOfferList
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
