from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.margin_readiness import MarginReadiness
from ...types import UNSET, Response


def _get_kwargs(
    *,
    variant_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_variant_id = str(variant_id)
    params["variantId"] = json_variant_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/cost/readiness",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | MarginReadiness:
    if response.status_code == 200:
        response_200 = MarginReadiness.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | MarginReadiness]:
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
) -> Response[ErrorEnvelope | MarginReadiness]:
    """Read a SKU's margin readiness.

     Returns the derived margin readiness for a variant (CST-003): one of Complete, Partial, Stale,
    Missing, with the components that block or limit it. Only Complete drives an executable
    recommendation (enforced downstream); Partial may show analysis but no approval control.

    Args:
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarginReadiness]
    """

    kwargs = _get_kwargs(
        variant_id=variant_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
) -> ErrorEnvelope | MarginReadiness | None:
    """Read a SKU's margin readiness.

     Returns the derived margin readiness for a variant (CST-003): one of Complete, Partial, Stale,
    Missing, with the components that block or limit it. Only Complete drives an executable
    recommendation (enforced downstream); Partial may show analysis but no approval control.

    Args:
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarginReadiness
    """

    return sync_detailed(
        client=client,
        variant_id=variant_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
) -> Response[ErrorEnvelope | MarginReadiness]:
    """Read a SKU's margin readiness.

     Returns the derived margin readiness for a variant (CST-003): one of Complete, Partial, Stale,
    Missing, with the components that block or limit it. Only Complete drives an executable
    recommendation (enforced downstream); Partial may show analysis but no approval control.

    Args:
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarginReadiness]
    """

    kwargs = _get_kwargs(
        variant_id=variant_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    variant_id: UUID,
) -> ErrorEnvelope | MarginReadiness | None:
    """Read a SKU's margin readiness.

     Returns the derived margin readiness for a variant (CST-003): one of Complete, Partial, Stale,
    Missing, with the components that block or limit it. Only Complete drives an executable
    recommendation (enforced downstream); Partial may show analysis but no approval control.

    Args:
        variant_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarginReadiness
    """

    return (
        await asyncio_detailed(
            client=client,
            variant_id=variant_id,
        )
    ).parsed
