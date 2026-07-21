from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.listing_diagnostics_report import ListingDiagnosticsReport
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
        "url": "/catalog/product-diagnostics",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | ListingDiagnosticsReport:
    if response.status_code == 200:
        response_200 = ListingDiagnosticsReport.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | ListingDiagnosticsReport]:
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
    variant_id: UUID,
) -> Response[ErrorEnvelope | ListingDiagnosticsReport]:
    """Read READ-ONLY listing and image diagnostics for a variant.

     The listing/image diagnostics report for one variant (S26, LST-001). Diagnostics are STRICTLY READ-
    ONLY: every result is DERIVED from already captured canonical catalog data (Product / Variant /
    Listing) and NEVER generates, rewrites, or publishes content — there is no write/execute control on
    this seam anywhere. Each result NAMES the observed entity + field and the rule id/version it was
    evaluated against (LST-001), carries observed-value METADATA only (presence/length — never the raw
    text or a fabricated value; quarantine-over-inference), a pass/warn result, a stable evidence
    reference, and the capture time of the underlying catalog data. A field whose source content the
    connector does not yet surface is reported observed-state not_observed → warn (fail closed, never a
    fabricated pass). Account scope is org-derived and fails closed cross-account (a foreign or unknown
    variant is 404); possession of an account UUID grants no access.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ListingDiagnosticsReport]
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
    client: Client,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> ErrorEnvelope | ListingDiagnosticsReport | None:
    """Read READ-ONLY listing and image diagnostics for a variant.

     The listing/image diagnostics report for one variant (S26, LST-001). Diagnostics are STRICTLY READ-
    ONLY: every result is DERIVED from already captured canonical catalog data (Product / Variant /
    Listing) and NEVER generates, rewrites, or publishes content — there is no write/execute control on
    this seam anywhere. Each result NAMES the observed entity + field and the rule id/version it was
    evaluated against (LST-001), carries observed-value METADATA only (presence/length — never the raw
    text or a fabricated value; quarantine-over-inference), a pass/warn result, a stable evidence
    reference, and the capture time of the underlying catalog data. A field whose source content the
    connector does not yet surface is reported observed-state not_observed → warn (fail closed, never a
    fabricated pass). Account scope is org-derived and fails closed cross-account (a foreign or unknown
    variant is 404); possession of an account UUID grants no access.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ListingDiagnosticsReport
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        variant_id=variant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> Response[ErrorEnvelope | ListingDiagnosticsReport]:
    """Read READ-ONLY listing and image diagnostics for a variant.

     The listing/image diagnostics report for one variant (S26, LST-001). Diagnostics are STRICTLY READ-
    ONLY: every result is DERIVED from already captured canonical catalog data (Product / Variant /
    Listing) and NEVER generates, rewrites, or publishes content — there is no write/execute control on
    this seam anywhere. Each result NAMES the observed entity + field and the rule id/version it was
    evaluated against (LST-001), carries observed-value METADATA only (presence/length — never the raw
    text or a fabricated value; quarantine-over-inference), a pass/warn result, a stable evidence
    reference, and the capture time of the underlying catalog data. A field whose source content the
    connector does not yet surface is reported observed-state not_observed → warn (fail closed, never a
    fabricated pass). Account scope is org-derived and fails closed cross-account (a foreign or unknown
    variant is 404); possession of an account UUID grants no access.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ListingDiagnosticsReport]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        variant_id=variant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
    variant_id: UUID,
) -> ErrorEnvelope | ListingDiagnosticsReport | None:
    """Read READ-ONLY listing and image diagnostics for a variant.

     The listing/image diagnostics report for one variant (S26, LST-001). Diagnostics are STRICTLY READ-
    ONLY: every result is DERIVED from already captured canonical catalog data (Product / Variant /
    Listing) and NEVER generates, rewrites, or publishes content — there is no write/execute control on
    this seam anywhere. Each result NAMES the observed entity + field and the rule id/version it was
    evaluated against (LST-001), carries observed-value METADATA only (presence/length — never the raw
    text or a fabricated value; quarantine-over-inference), a pass/warn result, a stable evidence
    reference, and the capture time of the underlying catalog data. A field whose source content the
    connector does not yet surface is reported observed-state not_observed → warn (fail closed, never a
    fabricated pass). Account scope is org-derived and fails closed cross-account (a foreign or unknown
    variant is 404); possession of an account UUID grants no access.

    Args:
        marketplace_account_id (UUID):
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ListingDiagnosticsReport
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
        )
    ).parsed
