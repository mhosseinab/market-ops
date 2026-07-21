from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.market_event import MarketEvent
from ...types import UNSET, Response


def _get_kwargs(
    *,
    event_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_event_id = str(event_id)
    params["eventId"] = json_event_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/event",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | MarketEvent:
    if response.status_code == 200:
        response_200 = MarketEvent.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | MarketEvent]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    event_id: UUID,
) -> Response[ErrorEnvelope | MarketEvent]:
    """Get a single market event by id.

     Returns one market event with its full lifecycle, ranking factors, exposure (known Money or
    explicitly unknown, EVT-005), and cited evidence (PRD §7.4). The threshold version that fired it is
    included for reproducibility (EVT-002).

    Args:
        event_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarketEvent]
    """

    kwargs = _get_kwargs(
        event_id=event_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    event_id: UUID,
) -> ErrorEnvelope | MarketEvent | None:
    """Get a single market event by id.

     Returns one market event with its full lifecycle, ranking factors, exposure (known Money or
    explicitly unknown, EVT-005), and cited evidence (PRD §7.4). The threshold version that fired it is
    included for reproducibility (EVT-002).

    Args:
        event_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarketEvent
    """

    return sync_detailed(
        client=client,
        event_id=event_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    event_id: UUID,
) -> Response[ErrorEnvelope | MarketEvent]:
    """Get a single market event by id.

     Returns one market event with its full lifecycle, ranking factors, exposure (known Money or
    explicitly unknown, EVT-005), and cited evidence (PRD §7.4). The threshold version that fired it is
    included for reproducibility (EVT-002).

    Args:
        event_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarketEvent]
    """

    kwargs = _get_kwargs(
        event_id=event_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    event_id: UUID,
) -> ErrorEnvelope | MarketEvent | None:
    """Get a single market event by id.

     Returns one market event with its full lifecycle, ranking factors, exposure (known Money or
    explicitly unknown, EVT-005), and cited evidence (PRD §7.4). The threshold version that fired it is
    included for reproducibility (EVT-002).

    Args:
        event_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarketEvent
    """

    return (
        await asyncio_detailed(
            client=client,
            event_id=event_id,
        )
    ).parsed
