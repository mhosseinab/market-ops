from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.observation_list import ObservationList
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    target_id: UUID,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_target_id = str(target_id)
    params["targetId"] = json_target_id

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/observation/observations",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | ObservationList:
    if response.status_code == 200:
        response_200 = ObservationList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | ObservationList]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    target_id: UUID,
    limit: int | Unset = UNSET,
) -> Response[ErrorEnvelope | ObservationList]:
    """List append-only observation evidence for a target.

     Returns the append-only observation evidence for one target, newest first (PRD §7.3
    OBS-002/OBS-004). Historical values never silently become current: each row carries its captured
    time, quality, and freshness deadline so an expired value renders with age and cannot satisfy a
    current-data gate.

    Args:
        target_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ObservationList]
    """

    kwargs = _get_kwargs(
        target_id=target_id,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    target_id: UUID,
    limit: int | Unset = UNSET,
) -> ErrorEnvelope | ObservationList | None:
    """List append-only observation evidence for a target.

     Returns the append-only observation evidence for one target, newest first (PRD §7.3
    OBS-002/OBS-004). Historical values never silently become current: each row carries its captured
    time, quality, and freshness deadline so an expired value renders with age and cannot satisfy a
    current-data gate.

    Args:
        target_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ObservationList
    """

    return sync_detailed(
        client=client,
        target_id=target_id,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    target_id: UUID,
    limit: int | Unset = UNSET,
) -> Response[ErrorEnvelope | ObservationList]:
    """List append-only observation evidence for a target.

     Returns the append-only observation evidence for one target, newest first (PRD §7.3
    OBS-002/OBS-004). Historical values never silently become current: each row carries its captured
    time, quality, and freshness deadline so an expired value renders with age and cannot satisfy a
    current-data gate.

    Args:
        target_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ObservationList]
    """

    kwargs = _get_kwargs(
        target_id=target_id,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    target_id: UUID,
    limit: int | Unset = UNSET,
) -> ErrorEnvelope | ObservationList | None:
    """List append-only observation evidence for a target.

     Returns the append-only observation evidence for one target, newest first (PRD §7.3
    OBS-002/OBS-004). Historical values never silently become current: each row carries its captured
    time, quality, and freshness deadline so an expired value renders with age and cannot satisfy a
    current-data gate.

    Args:
        target_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ObservationList
    """

    return (
        await asyncio_detailed(
            client=client,
            target_id=target_id,
            limit=limit,
        )
    ).parsed
