from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.catalog_product_row import CatalogProductRow
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    json_variant_id = str(variant_id)
    params["variantId"] = json_variant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/catalog/product",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CatalogProductRow | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CatalogProductRow.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CatalogProductRow | ErrorEnvelope]:
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
    variant_id: UUID,
) -> Response[CatalogProductRow | ErrorEnvelope]:
    """Read one canonical Product row for a variant.

     The single-variant canonical Product read backing Product detail (PRD §6.1). Same canonical,
    capability-gated row as listCatalogProducts, scoped to one variant. Owned-offer data renders only
    when owned_offer_read is Supported; otherwise a reason is carried (Unknown never enables). Account
    scope fails closed cross-account; an unknown or foreign variant is 404.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CatalogProductRow | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        variant_id=variant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> CatalogProductRow | ErrorEnvelope | None:
    """Read one canonical Product row for a variant.

     The single-variant canonical Product read backing Product detail (PRD §6.1). Same canonical,
    capability-gated row as listCatalogProducts, scoped to one variant. Owned-offer data renders only
    when owned_offer_read is Supported; otherwise a reason is carried (Unknown never enables). Account
    scope fails closed cross-account; an unknown or foreign variant is 404.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CatalogProductRow | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        variant_id=variant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> Response[CatalogProductRow | ErrorEnvelope]:
    """Read one canonical Product row for a variant.

     The single-variant canonical Product read backing Product detail (PRD §6.1). Same canonical,
    capability-gated row as listCatalogProducts, scoped to one variant. Owned-offer data renders only
    when owned_offer_read is Supported; otherwise a reason is carried (Unknown never enables). Account
    scope fails closed cross-account; an unknown or foreign variant is 404.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CatalogProductRow | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        variant_id=variant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> CatalogProductRow | ErrorEnvelope | None:
    """Read one canonical Product row for a variant.

     The single-variant canonical Product read backing Product detail (PRD §6.1). Same canonical,
    capability-gated row as listCatalogProducts, scoped to one variant. Owned-offer data renders only
    when owned_offer_read is Supported; otherwise a reason is carried (Unknown never enables). Account
    scope fails closed cross-account; an unknown or foreign variant is 404.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CatalogProductRow | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
        )
    ).parsed
