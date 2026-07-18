from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
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
        "url": "/observation/observed-offers",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | ObservedOfferList:
    if response.status_code == 200:
        response_200 = ObservedOfferList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | ObservedOfferList]:
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
) -> Response[ErrorEnvelope | ObservedOfferList]:
    """List the account's derived current Observed Offers.

     Returns the derived CURRENT view of the observed market (PRD §7.3, §10.3): one Observed Offer per
    (target, offer identity), carrying its quality state, the raw price evidence (money quarantine —
    never a Money), the freshness deadline, and the corroborating route provenance. An expired offer is
    Stale and renders age-only; a disappeared offer is closed with an end time and is never a zero price
    (§16).

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
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | ObservedOfferList | None:
    """List the account's derived current Observed Offers.

     Returns the derived CURRENT view of the observed market (PRD §7.3, §10.3): one Observed Offer per
    (target, offer identity), carrying its quality state, the raw price evidence (money quarantine —
    never a Money), the freshness deadline, and the corroborating route provenance. An expired offer is
    Stale and renders age-only; a disappeared offer is closed with an end time and is never a zero price
    (§16).

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
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | ObservedOfferList]:
    """List the account's derived current Observed Offers.

     Returns the derived CURRENT view of the observed market (PRD §7.3, §10.3): one Observed Offer per
    (target, offer identity), carrying its quality state, the raw price evidence (money quarantine —
    never a Money), the freshness deadline, and the corroborating route provenance. An expired offer is
    Stale and renders age-only; a disappeared offer is closed with an end time and is never a zero price
    (§16).

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
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | ObservedOfferList | None:
    """List the account's derived current Observed Offers.

     Returns the derived CURRENT view of the observed market (PRD §7.3, §10.3): one Observed Offer per
    (target, offer identity), carrying its quality state, the raw price evidence (money quarantine —
    never a Money), the freshness deadline, and the corroborating route provenance. An expired offer is
    Stale and renders age-only; a disappeared offer is closed with an end time and is never a zero price
    (§16).

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
