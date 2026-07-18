from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.cost_profile_version import CostProfileVersion
from ...models.error_envelope import ErrorEnvelope
from ...models.single_cost_entry_request import SingleCostEntryRequest
from ...types import Response


def _get_kwargs(
    *,
    body: SingleCostEntryRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/cost/value",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CostProfileVersion | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CostProfileVersion.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CostProfileVersion | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SingleCostEntryRequest,
) -> Response[CostProfileVersion | ErrorEnvelope]:
    """Record a single cost-component value.

     Records ONE component value for a SKU as a new append-only cost-profile version and recomputes
    readiness (CST-002/CST-003). Used by the guided cost-blocker flow. The value is parsed to an exact
    integer money amount (no float, §9.1); the raw entered text/unit is preserved as evidence.

    Args:
        body (SingleCostEntryRequest): Record one cost-component value for a SKU (CST-002).
            effectiveFrom defaults to now; staleAfter is an optional review-by instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostProfileVersion | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    body: SingleCostEntryRequest,
) -> CostProfileVersion | ErrorEnvelope | None:
    """Record a single cost-component value.

     Records ONE component value for a SKU as a new append-only cost-profile version and recomputes
    readiness (CST-002/CST-003). Used by the guided cost-blocker flow. The value is parsed to an exact
    integer money amount (no float, §9.1); the raw entered text/unit is preserved as evidence.

    Args:
        body (SingleCostEntryRequest): Record one cost-component value for a SKU (CST-002).
            effectiveFrom defaults to now; staleAfter is an optional review-by instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostProfileVersion | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: SingleCostEntryRequest,
) -> Response[CostProfileVersion | ErrorEnvelope]:
    """Record a single cost-component value.

     Records ONE component value for a SKU as a new append-only cost-profile version and recomputes
    readiness (CST-002/CST-003). Used by the guided cost-blocker flow. The value is parsed to an exact
    integer money amount (no float, §9.1); the raw entered text/unit is preserved as evidence.

    Args:
        body (SingleCostEntryRequest): Record one cost-component value for a SKU (CST-002).
            effectiveFrom defaults to now; staleAfter is an optional review-by instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostProfileVersion | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: SingleCostEntryRequest,
) -> CostProfileVersion | ErrorEnvelope | None:
    """Record a single cost-component value.

     Records ONE component value for a SKU as a new append-only cost-profile version and recomputes
    readiness (CST-002/CST-003). Used by the guided cost-blocker flow. The value is parsed to an exact
    integer money amount (no float, §9.1); the raw entered text/unit is preserved as evidence.

    Args:
        body (SingleCostEntryRequest): Record one cost-component value for a SKU (CST-002).
            effectiveFrom defaults to now; staleAfter is an optional review-by instant.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostProfileVersion | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
