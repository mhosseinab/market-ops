from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.health import Health
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/healthz",
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | Health:
    if response.status_code == 200:
        response_200 = Health.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | Health]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
) -> Response[ErrorEnvelope | Health]:
    """Liveness probe with build identity.

     Returns service liveness plus the running binary's build identity. Unauthenticated so orchestrators
    and load balancers can probe it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | Health]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
) -> ErrorEnvelope | Health | None:
    """Liveness probe with build identity.

     Returns service liveness plus the running binary's build identity. Unauthenticated so orchestrators
    and load balancers can probe it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | Health
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
) -> Response[ErrorEnvelope | Health]:
    """Liveness probe with build identity.

     Returns service liveness plus the running binary's build identity. Unauthenticated so orchestrators
    and load balancers can probe it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | Health]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
) -> ErrorEnvelope | Health | None:
    """Liveness probe with build identity.

     Returns service liveness plus the running binary's build identity. Unauthenticated so orchestrators
    and load balancers can probe it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | Health
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
