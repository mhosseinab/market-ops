from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.action_execution_view import ActionExecutionView
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response


def _get_kwargs(
    *,
    action_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_action_id = str(action_id)
    params["actionId"] = json_action_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/actions/execution",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ActionExecutionView | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = ActionExecutionView.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ActionExecutionView | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    action_id: UUID,
) -> Response[ActionExecutionView | ErrorEnvelope]:
    """Get an action's execution record (CHAT-073).

     Returns the single execution record for an action (EXE-002): its mode, external state (EXE-003),
    external ref, and reconciliation instant. This is a read; it never writes or advances state.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ActionExecutionView | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        action_id=action_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    action_id: UUID,
) -> ActionExecutionView | ErrorEnvelope | None:
    """Get an action's execution record (CHAT-073).

     Returns the single execution record for an action (EXE-002): its mode, external state (EXE-003),
    external ref, and reconciliation instant. This is a read; it never writes or advances state.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ActionExecutionView | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        action_id=action_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    action_id: UUID,
) -> Response[ActionExecutionView | ErrorEnvelope]:
    """Get an action's execution record (CHAT-073).

     Returns the single execution record for an action (EXE-002): its mode, external state (EXE-003),
    external ref, and reconciliation instant. This is a read; it never writes or advances state.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ActionExecutionView | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        action_id=action_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    action_id: UUID,
) -> ActionExecutionView | ErrorEnvelope | None:
    """Get an action's execution record (CHAT-073).

     Returns the single execution record for an action (EXE-002): its mode, external state (EXE-003),
    external ref, and reconciliation instant. This is a read; it never writes or advances state.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ActionExecutionView | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            action_id=action_id,
        )
    ).parsed
