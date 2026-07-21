from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.execute_action_request import ExecuteActionRequest
from ...models.execute_action_result import ExecuteActionResult
from ...types import Response


def _get_kwargs(
    *,
    body: ExecuteActionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/actions/execute",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | ExecuteActionResult:
    if response.status_code == 200:
        response_200 = ExecuteActionResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | ExecuteActionResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: ExecuteActionRequest,
) -> Response[ErrorEnvelope | ExecuteActionResult]:
    """Revalidate and execute an approved action (EXE-001/002/005).

     The L4 execution path (PRD §7.5). It re-resolves the current binding SERVER-SIDE and runs the
    EXE-001 nine-gate revalidation matrix (identity, current price, costs, money unit, boundary,
    evidence/JIT, guardrails, permission, expiry); an injected change in ANY gate prevents the write and
    invalidates the card. When gates pass AND writes are enabled (a Supported price_write capability AND
    the S35 region write-verification flag), it performs EXACTLY ONE idempotent write keyed by the
    card's stable idempotency key (EXE-002) and reports the external state (EXE-003). When writes are
    OFF (default), NOTHING is written: the action is tracked recommend-only (EXE-005). It is idempotent:
    a repeat call replays the recorded result with zero additional external writes.

    Args:
        body (ExecuteActionRequest): Request to revalidate and execute an approved card (§7.5).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ExecuteActionResult]
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
    body: ExecuteActionRequest,
) -> ErrorEnvelope | ExecuteActionResult | None:
    """Revalidate and execute an approved action (EXE-001/002/005).

     The L4 execution path (PRD §7.5). It re-resolves the current binding SERVER-SIDE and runs the
    EXE-001 nine-gate revalidation matrix (identity, current price, costs, money unit, boundary,
    evidence/JIT, guardrails, permission, expiry); an injected change in ANY gate prevents the write and
    invalidates the card. When gates pass AND writes are enabled (a Supported price_write capability AND
    the S35 region write-verification flag), it performs EXACTLY ONE idempotent write keyed by the
    card's stable idempotency key (EXE-002) and reports the external state (EXE-003). When writes are
    OFF (default), NOTHING is written: the action is tracked recommend-only (EXE-005). It is idempotent:
    a repeat call replays the recorded result with zero additional external writes.

    Args:
        body (ExecuteActionRequest): Request to revalidate and execute an approved card (§7.5).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ExecuteActionResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: ExecuteActionRequest,
) -> Response[ErrorEnvelope | ExecuteActionResult]:
    """Revalidate and execute an approved action (EXE-001/002/005).

     The L4 execution path (PRD §7.5). It re-resolves the current binding SERVER-SIDE and runs the
    EXE-001 nine-gate revalidation matrix (identity, current price, costs, money unit, boundary,
    evidence/JIT, guardrails, permission, expiry); an injected change in ANY gate prevents the write and
    invalidates the card. When gates pass AND writes are enabled (a Supported price_write capability AND
    the S35 region write-verification flag), it performs EXACTLY ONE idempotent write keyed by the
    card's stable idempotency key (EXE-002) and reports the external state (EXE-003). When writes are
    OFF (default), NOTHING is written: the action is tracked recommend-only (EXE-005). It is idempotent:
    a repeat call replays the recorded result with zero additional external writes.

    Args:
        body (ExecuteActionRequest): Request to revalidate and execute an approved card (§7.5).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ExecuteActionResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: ExecuteActionRequest,
) -> ErrorEnvelope | ExecuteActionResult | None:
    """Revalidate and execute an approved action (EXE-001/002/005).

     The L4 execution path (PRD §7.5). It re-resolves the current binding SERVER-SIDE and runs the
    EXE-001 nine-gate revalidation matrix (identity, current price, costs, money unit, boundary,
    evidence/JIT, guardrails, permission, expiry); an injected change in ANY gate prevents the write and
    invalidates the card. When gates pass AND writes are enabled (a Supported price_write capability AND
    the S35 region write-verification flag), it performs EXACTLY ONE idempotent write keyed by the
    card's stable idempotency key (EXE-002) and reports the external state (EXE-003). When writes are
    OFF (default), NOTHING is written: the action is tracked recommend-only (EXE-005). It is idempotent:
    a repeat call replays the recorded result with zero additional external writes.

    Args:
        body (ExecuteActionRequest): Request to revalidate and execute an approved card (§7.5).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ExecuteActionResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
