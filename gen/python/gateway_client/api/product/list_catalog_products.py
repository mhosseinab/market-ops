from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.catalog_product_page import CatalogProductPage
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
    cursor: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    params["cursor"] = cursor

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/catalog/products",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> CatalogProductPage | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CatalogProductPage.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[CatalogProductPage | ErrorEnvelope]:
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
    cursor: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[CatalogProductPage | ErrorEnvelope]:
    """List the account's canonical Products (SKU workspace rows).

     The account-scoped, cursor-paginated Products read model (PRD §6.1 CAT UI / journey 1). Each row is
    built from the CANONICAL Product/Variant/ Listing/Owned Offer entities — NEVER inferred from an
    observation target (a target is a dependent projection, not inventory). Every synced variant appears
    with its explicit identity MAPPING STATE (confirmed / needs_review / rejected / obsolete / unmapped)
    and whether it is WATCHED (an active Confirmed identity with an active observation target, OBS-001).
    Owned-offer data is CAPABILITY-GATED (owned_offer_read, §15.2): rendered only when Supported,
    otherwise a machine-readable reason is carried so the UI shows WHY it is unavailable — Unknown never
    enables dependent UI. The market snapshot is the target's current Observed Offers surfaced
    INDIVIDUALLY with their offer identity, ordered deterministically by offerIdentity ascending (a
    stable, non-money key). Money quarantine (§9.1) forbids numeric price ranking, so offers are NEVER
    collapsed into an anonymous lowest-competitor price. Pagination is by the stable native_variant_id
    key (never a mutable updated_at). Account scope fails closed cross-account.

    Args:
        marketplace_account_id (UUID):
        cursor (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CatalogProductPage | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        cursor=cursor,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    marketplace_account_id: UUID,
    cursor: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> CatalogProductPage | ErrorEnvelope | None:
    """List the account's canonical Products (SKU workspace rows).

     The account-scoped, cursor-paginated Products read model (PRD §6.1 CAT UI / journey 1). Each row is
    built from the CANONICAL Product/Variant/ Listing/Owned Offer entities — NEVER inferred from an
    observation target (a target is a dependent projection, not inventory). Every synced variant appears
    with its explicit identity MAPPING STATE (confirmed / needs_review / rejected / obsolete / unmapped)
    and whether it is WATCHED (an active Confirmed identity with an active observation target, OBS-001).
    Owned-offer data is CAPABILITY-GATED (owned_offer_read, §15.2): rendered only when Supported,
    otherwise a machine-readable reason is carried so the UI shows WHY it is unavailable — Unknown never
    enables dependent UI. The market snapshot is the target's current Observed Offers surfaced
    INDIVIDUALLY with their offer identity, ordered deterministically by offerIdentity ascending (a
    stable, non-money key). Money quarantine (§9.1) forbids numeric price ranking, so offers are NEVER
    collapsed into an anonymous lowest-competitor price. Pagination is by the stable native_variant_id
    key (never a mutable updated_at). Account scope fails closed cross-account.

    Args:
        marketplace_account_id (UUID):
        cursor (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CatalogProductPage | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        cursor=cursor,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    cursor: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[CatalogProductPage | ErrorEnvelope]:
    """List the account's canonical Products (SKU workspace rows).

     The account-scoped, cursor-paginated Products read model (PRD §6.1 CAT UI / journey 1). Each row is
    built from the CANONICAL Product/Variant/ Listing/Owned Offer entities — NEVER inferred from an
    observation target (a target is a dependent projection, not inventory). Every synced variant appears
    with its explicit identity MAPPING STATE (confirmed / needs_review / rejected / obsolete / unmapped)
    and whether it is WATCHED (an active Confirmed identity with an active observation target, OBS-001).
    Owned-offer data is CAPABILITY-GATED (owned_offer_read, §15.2): rendered only when Supported,
    otherwise a machine-readable reason is carried so the UI shows WHY it is unavailable — Unknown never
    enables dependent UI. The market snapshot is the target's current Observed Offers surfaced
    INDIVIDUALLY with their offer identity, ordered deterministically by offerIdentity ascending (a
    stable, non-money key). Money quarantine (§9.1) forbids numeric price ranking, so offers are NEVER
    collapsed into an anonymous lowest-competitor price. Pagination is by the stable native_variant_id
    key (never a mutable updated_at). Account scope fails closed cross-account.

    Args:
        marketplace_account_id (UUID):
        cursor (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CatalogProductPage | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        cursor=cursor,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
    cursor: str | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> CatalogProductPage | ErrorEnvelope | None:
    """List the account's canonical Products (SKU workspace rows).

     The account-scoped, cursor-paginated Products read model (PRD §6.1 CAT UI / journey 1). Each row is
    built from the CANONICAL Product/Variant/ Listing/Owned Offer entities — NEVER inferred from an
    observation target (a target is a dependent projection, not inventory). Every synced variant appears
    with its explicit identity MAPPING STATE (confirmed / needs_review / rejected / obsolete / unmapped)
    and whether it is WATCHED (an active Confirmed identity with an active observation target, OBS-001).
    Owned-offer data is CAPABILITY-GATED (owned_offer_read, §15.2): rendered only when Supported,
    otherwise a machine-readable reason is carried so the UI shows WHY it is unavailable — Unknown never
    enables dependent UI. The market snapshot is the target's current Observed Offers surfaced
    INDIVIDUALLY with their offer identity, ordered deterministically by offerIdentity ascending (a
    stable, non-money key). Money quarantine (§9.1) forbids numeric price ranking, so offers are NEVER
    collapsed into an anonymous lowest-competitor price. Pagination is by the stable native_variant_id
    key (never a mutable updated_at). Account scope fails closed cross-account.

    Args:
        marketplace_account_id (UUID):
        cursor (str | Unset):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CatalogProductPage | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            cursor=cursor,
            limit=limit,
        )
    ).parsed
