from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.retry_action_request import RetryActionRequest
from ...models.retry_action_result import RetryActionResult
from ...types import Response


def _get_kwargs(
    *,
    body: RetryActionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/actions/retry",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | RetryActionResult:
    if response.status_code == 200:
        response_200 = RetryActionResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | RetryActionResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: RetryActionRequest,
) -> Response[ErrorEnvelope | RetryActionResult]:
    r"""Retry an eligible failed action (EXE-003, CHAT-074).

     Gates a retry. It REJECTS an action whose execution is still Pending Reconciliation — an unknown
    result must reconcile first, never be retried (EXE-003 / CHAT-074). A definitively Failed action is
    retry-eligible (§16 \"retry only eligible reconciled failures\"); the actual re-write proceeds only
    through a fresh approved action (rollback/retry is never an automatic inverse or duplicate write,
    EXE-004).

    Args:
        body (RetryActionRequest): Request to retry an eligible failed action (EXE-003 /
            CHAT-074).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | RetryActionResult]
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
    body: RetryActionRequest,
) -> ErrorEnvelope | RetryActionResult | None:
    r"""Retry an eligible failed action (EXE-003, CHAT-074).

     Gates a retry. It REJECTS an action whose execution is still Pending Reconciliation — an unknown
    result must reconcile first, never be retried (EXE-003 / CHAT-074). A definitively Failed action is
    retry-eligible (§16 \"retry only eligible reconciled failures\"); the actual re-write proceeds only
    through a fresh approved action (rollback/retry is never an automatic inverse or duplicate write,
    EXE-004).

    Args:
        body (RetryActionRequest): Request to retry an eligible failed action (EXE-003 /
            CHAT-074).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | RetryActionResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: RetryActionRequest,
) -> Response[ErrorEnvelope | RetryActionResult]:
    r"""Retry an eligible failed action (EXE-003, CHAT-074).

     Gates a retry. It REJECTS an action whose execution is still Pending Reconciliation — an unknown
    result must reconcile first, never be retried (EXE-003 / CHAT-074). A definitively Failed action is
    retry-eligible (§16 \"retry only eligible reconciled failures\"); the actual re-write proceeds only
    through a fresh approved action (rollback/retry is never an automatic inverse or duplicate write,
    EXE-004).

    Args:
        body (RetryActionRequest): Request to retry an eligible failed action (EXE-003 /
            CHAT-074).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | RetryActionResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: RetryActionRequest,
) -> ErrorEnvelope | RetryActionResult | None:
    r"""Retry an eligible failed action (EXE-003, CHAT-074).

     Gates a retry. It REJECTS an action whose execution is still Pending Reconciliation — an unknown
    result must reconcile first, never be retried (EXE-003 / CHAT-074). A definitively Failed action is
    retry-eligible (§16 \"retry only eligible reconciled failures\"); the actual re-write proceeds only
    through a fresh approved action (rollback/retry is never an automatic inverse or duplicate write,
    EXE-004).

    Args:
        body (RetryActionRequest): Request to retry an eligible failed action (EXE-003 /
            CHAT-074).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | RetryActionResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
