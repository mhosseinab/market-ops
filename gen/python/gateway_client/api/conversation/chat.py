from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.chat_turn_request import ChatTurnRequest
from ...models.chat_unavailable import ChatUnavailable
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: ChatTurnRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/chat",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ChatUnavailable | ErrorEnvelope | str:
    if response.status_code == 200:
        response_200 = response.text
        return response_200

    if response.status_code == 503:
        response_503 = ChatUnavailable.from_dict(response.json())

        return response_503

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ChatUnavailable | ErrorEnvelope | str]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: ChatTurnRequest,
) -> Response[ChatUnavailable | ErrorEnvelope | str]:
    """Converse with the LLM plane over a Server-Sent Events stream.

     Opens (or continues) a conversation turn and streams the LLM plane's response as Server-Sent Events
    (PRD §19.3: SSE, no WebSocket). The gateway authenticates the browser session (cookie), authorizes
    the read action through the single shared permission matrix, and proxies the turn to the internal
    Python LLM service; it never lets free text approve, execute, or confirm anything (PRD §8, §12.3 —
    the LLM plane holds a read/Draft-only credential and no model tool advances an action past Draft).
    When the global or per-account kill switch is on, or the LLM plane is unreachable, chat returns a
    structured unavailable state and NOTHING else degrades — every structured screen stays fully
    functional (CHAT-009).

    Args:
        body (ChatTurnRequest): One conversation turn from the browser. The message is free text
            and carries NO authority (PRD §8 free-text containment): it can never approve, execute, or
            confirm — those live only in structured controls outside the model plane. A turn
            optionally continues an existing conversation and/or binds a marketplace-account context;
            context resolution itself is deterministic in the LLM plane (§8.1), never guessed from
            this field.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ChatUnavailable | ErrorEnvelope | str]
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
    body: ChatTurnRequest,
) -> ChatUnavailable | ErrorEnvelope | str | None:
    """Converse with the LLM plane over a Server-Sent Events stream.

     Opens (or continues) a conversation turn and streams the LLM plane's response as Server-Sent Events
    (PRD §19.3: SSE, no WebSocket). The gateway authenticates the browser session (cookie), authorizes
    the read action through the single shared permission matrix, and proxies the turn to the internal
    Python LLM service; it never lets free text approve, execute, or confirm anything (PRD §8, §12.3 —
    the LLM plane holds a read/Draft-only credential and no model tool advances an action past Draft).
    When the global or per-account kill switch is on, or the LLM plane is unreachable, chat returns a
    structured unavailable state and NOTHING else degrades — every structured screen stays fully
    functional (CHAT-009).

    Args:
        body (ChatTurnRequest): One conversation turn from the browser. The message is free text
            and carries NO authority (PRD §8 free-text containment): it can never approve, execute, or
            confirm — those live only in structured controls outside the model plane. A turn
            optionally continues an existing conversation and/or binds a marketplace-account context;
            context resolution itself is deterministic in the LLM plane (§8.1), never guessed from
            this field.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ChatUnavailable | ErrorEnvelope | str
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: ChatTurnRequest,
) -> Response[ChatUnavailable | ErrorEnvelope | str]:
    """Converse with the LLM plane over a Server-Sent Events stream.

     Opens (or continues) a conversation turn and streams the LLM plane's response as Server-Sent Events
    (PRD §19.3: SSE, no WebSocket). The gateway authenticates the browser session (cookie), authorizes
    the read action through the single shared permission matrix, and proxies the turn to the internal
    Python LLM service; it never lets free text approve, execute, or confirm anything (PRD §8, §12.3 —
    the LLM plane holds a read/Draft-only credential and no model tool advances an action past Draft).
    When the global or per-account kill switch is on, or the LLM plane is unreachable, chat returns a
    structured unavailable state and NOTHING else degrades — every structured screen stays fully
    functional (CHAT-009).

    Args:
        body (ChatTurnRequest): One conversation turn from the browser. The message is free text
            and carries NO authority (PRD §8 free-text containment): it can never approve, execute, or
            confirm — those live only in structured controls outside the model plane. A turn
            optionally continues an existing conversation and/or binds a marketplace-account context;
            context resolution itself is deterministic in the LLM plane (§8.1), never guessed from
            this field.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ChatUnavailable | ErrorEnvelope | str]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: ChatTurnRequest,
) -> ChatUnavailable | ErrorEnvelope | str | None:
    """Converse with the LLM plane over a Server-Sent Events stream.

     Opens (or continues) a conversation turn and streams the LLM plane's response as Server-Sent Events
    (PRD §19.3: SSE, no WebSocket). The gateway authenticates the browser session (cookie), authorizes
    the read action through the single shared permission matrix, and proxies the turn to the internal
    Python LLM service; it never lets free text approve, execute, or confirm anything (PRD §8, §12.3 —
    the LLM plane holds a read/Draft-only credential and no model tool advances an action past Draft).
    When the global or per-account kill switch is on, or the LLM plane is unreachable, chat returns a
    structured unavailable state and NOTHING else degrades — every structured screen stays fully
    functional (CHAT-009).

    Args:
        body (ChatTurnRequest): One conversation turn from the browser. The message is free text
            and carries NO authority (PRD §8 free-text containment): it can never approve, execute, or
            confirm — those live only in structured controls outside the model plane. A turn
            optionally continues an existing conversation and/or binds a marketplace-account context;
            context resolution itself is deterministic in the LLM plane (§8.1), never guessed from
            this field.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ChatUnavailable | ErrorEnvelope | str
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
