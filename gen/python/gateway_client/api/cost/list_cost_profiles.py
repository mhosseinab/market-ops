import datetime
from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.cost_profile_list import CostProfileList
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    variant_id: UUID,
    as_of: datetime.datetime | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_variant_id = str(variant_id)
    params["variantId"] = json_variant_id

    json_as_of: str | Unset = UNSET
    if not isinstance(as_of, Unset):
        json_as_of = as_of.isoformat()
    params["asOf"] = json_as_of

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/cost/profiles",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CostProfileList | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CostProfileList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CostProfileList | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
    as_of: datetime.datetime | Unset = UNSET,
) -> Response[CostProfileList | ErrorEnvelope]:
    """List the in-force cost-profile versions for a SKU at a time.

     Returns the EXACT in-force version of each cost component for a variant at `asOf` (CST-002 point-in-
    time lookup). Omitting `asOf` returns the current in-force versions. This reproduces the cost
    profile that produced a historical number, never the current one.

    Args:
        variant_id (UUID):
        as_of (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostProfileList | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        variant_id=variant_id,
        as_of=as_of,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
    as_of: datetime.datetime | Unset = UNSET,
) -> CostProfileList | ErrorEnvelope | None:
    """List the in-force cost-profile versions for a SKU at a time.

     Returns the EXACT in-force version of each cost component for a variant at `asOf` (CST-002 point-in-
    time lookup). Omitting `asOf` returns the current in-force versions. This reproduces the cost
    profile that produced a historical number, never the current one.

    Args:
        variant_id (UUID):
        as_of (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostProfileList | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        variant_id=variant_id,
        as_of=as_of,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
    as_of: datetime.datetime | Unset = UNSET,
) -> Response[CostProfileList | ErrorEnvelope]:
    """List the in-force cost-profile versions for a SKU at a time.

     Returns the EXACT in-force version of each cost component for a variant at `asOf` (CST-002 point-in-
    time lookup). Omitting `asOf` returns the current in-force versions. This reproduces the cost
    profile that produced a historical number, never the current one.

    Args:
        variant_id (UUID):
        as_of (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostProfileList | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        variant_id=variant_id,
        as_of=as_of,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
    as_of: datetime.datetime | Unset = UNSET,
) -> CostProfileList | ErrorEnvelope | None:
    """List the in-force cost-profile versions for a SKU at a time.

     Returns the EXACT in-force version of each cost component for a variant at `asOf` (CST-002 point-in-
    time lookup). Omitting `asOf` returns the current in-force versions. This reproduces the cost
    profile that produced a historical number, never the current one.

    Args:
        variant_id (UUID):
        as_of (datetime.datetime | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostProfileList | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            variant_id=variant_id,
            as_of=as_of,
        )
    ).parsed
