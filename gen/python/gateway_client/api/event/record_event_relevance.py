from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.event_relevance_recorded import EventRelevanceRecorded
from ...models.event_relevance_request import EventRelevanceRequest
from ...types import Response


def _get_kwargs(
    *,
    body: EventRelevanceRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/events/relevance",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | EventRelevanceRecorded:
    if response.status_code == 202:
        response_202 = EventRelevanceRecorded.from_dict(response.json())

        return response_202

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | EventRelevanceRecorded]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: EventRelevanceRequest,
) -> Response[ErrorEnvelope | EventRelevanceRecorded]:
    r"""Record relevance feedback on a market event.

     Appends a relevance-feedback record for a market event (PRD §7.4 EVT-005 \"relevance feedback is
    stored\"). Feedback is APPEND-ONLY history — a mute is a feedback record, never a deletion of the
    event. This is a reversible seller-data write (L2, Owner/Operator); it never approves or executes
    anything.

    Args:
        body (EventRelevanceRequest): Record relevance feedback on a market event (EVT-005,
            append-only). Never approves or executes anything.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | EventRelevanceRecorded]
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
    client: Client,
    body: EventRelevanceRequest,
) -> ErrorEnvelope | EventRelevanceRecorded | None:
    r"""Record relevance feedback on a market event.

     Appends a relevance-feedback record for a market event (PRD §7.4 EVT-005 \"relevance feedback is
    stored\"). Feedback is APPEND-ONLY history — a mute is a feedback record, never a deletion of the
    event. This is a reversible seller-data write (L2, Owner/Operator); it never approves or executes
    anything.

    Args:
        body (EventRelevanceRequest): Record relevance feedback on a market event (EVT-005,
            append-only). Never approves or executes anything.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | EventRelevanceRecorded
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: EventRelevanceRequest,
) -> Response[ErrorEnvelope | EventRelevanceRecorded]:
    r"""Record relevance feedback on a market event.

     Appends a relevance-feedback record for a market event (PRD §7.4 EVT-005 \"relevance feedback is
    stored\"). Feedback is APPEND-ONLY history — a mute is a feedback record, never a deletion of the
    event. This is a reversible seller-data write (L2, Owner/Operator); it never approves or executes
    anything.

    Args:
        body (EventRelevanceRequest): Record relevance feedback on a market event (EVT-005,
            append-only). Never approves or executes anything.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | EventRelevanceRecorded]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: EventRelevanceRequest,
) -> ErrorEnvelope | EventRelevanceRecorded | None:
    r"""Record relevance feedback on a market event.

     Appends a relevance-feedback record for a market event (PRD §7.4 EVT-005 \"relevance feedback is
    stored\"). Feedback is APPEND-ONLY history — a mute is a feedback record, never a deletion of the
    event. This is a reversible seller-data write (L2, Owner/Operator); it never approves or executes
    anything.

    Args:
        body (EventRelevanceRequest): Record relevance feedback on a market event (EVT-005,
            append-only). Never approves or executes anything.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | EventRelevanceRecorded
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
