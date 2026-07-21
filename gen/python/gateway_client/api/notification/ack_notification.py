from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.notification_ack_request import NotificationAckRequest
from ...models.notification_ack_result import NotificationAckResult
from ...types import Response


def _get_kwargs(
    *,
    body: NotificationAckRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/notifications/ack",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | NotificationAckResult:
    if response.status_code == 200:
        response_200 = NotificationAckResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | NotificationAckResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: NotificationAckRequest,
) -> Response[ErrorEnvelope | NotificationAckResult]:
    """Acknowledge (mark read) one in-app notification.

     Marks one in-app notification read (PRD §7.5). read_at is a BOUNDED read-state projection advanced
    by a FROM-guarded update — the notification row itself is append-only, never overwritten.
    Acknowledgement is idempotent: acking an already-read or foreign notification is a no-op
    (changed=false), never an error and never a duplicate write.

    Args:
        body (NotificationAckRequest): Acknowledge (mark read) one notification for an account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | NotificationAckResult]
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
    body: NotificationAckRequest,
) -> ErrorEnvelope | NotificationAckResult | None:
    """Acknowledge (mark read) one in-app notification.

     Marks one in-app notification read (PRD §7.5). read_at is a BOUNDED read-state projection advanced
    by a FROM-guarded update — the notification row itself is append-only, never overwritten.
    Acknowledgement is idempotent: acking an already-read or foreign notification is a no-op
    (changed=false), never an error and never a duplicate write.

    Args:
        body (NotificationAckRequest): Acknowledge (mark read) one notification for an account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | NotificationAckResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: NotificationAckRequest,
) -> Response[ErrorEnvelope | NotificationAckResult]:
    """Acknowledge (mark read) one in-app notification.

     Marks one in-app notification read (PRD §7.5). read_at is a BOUNDED read-state projection advanced
    by a FROM-guarded update — the notification row itself is append-only, never overwritten.
    Acknowledgement is idempotent: acking an already-read or foreign notification is a no-op
    (changed=false), never an error and never a duplicate write.

    Args:
        body (NotificationAckRequest): Acknowledge (mark read) one notification for an account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | NotificationAckResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: NotificationAckRequest,
) -> ErrorEnvelope | NotificationAckResult | None:
    """Acknowledge (mark read) one in-app notification.

     Marks one in-app notification read (PRD §7.5). read_at is a BOUNDED read-state projection advanced
    by a FROM-guarded update — the notification row itself is append-only, never overwritten.
    Acknowledgement is idempotent: acking an already-read or foreign notification is a no-op
    (changed=false), never an error and never a duplicate write.

    Args:
        body (NotificationAckRequest): Acknowledge (mark read) one notification for an account.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | NotificationAckResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
