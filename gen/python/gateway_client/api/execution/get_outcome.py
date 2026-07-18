from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.outcome_view import OutcomeView
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
        "url": "/outcomes",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ErrorEnvelope | OutcomeView:
    if response.status_code == 200:
        response_200 = OutcomeView.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | OutcomeView]:
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
) -> Response[ErrorEnvelope | OutcomeView]:
    """Get an action's seven-day outcome window and result (OUT-001).

     Returns the OUT-001 seven-day outcome window for a reconciled action and, once the window has
    closed, its §15.3 result and confidence (or Not Measurable when the required evidence is absent).
    This is a read.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | OutcomeView]
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
) -> ErrorEnvelope | OutcomeView | None:
    """Get an action's seven-day outcome window and result (OUT-001).

     Returns the OUT-001 seven-day outcome window for a reconciled action and, once the window has
    closed, its §15.3 result and confidence (or Not Measurable when the required evidence is absent).
    This is a read.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | OutcomeView
    """

    return sync_detailed(
        client=client,
        action_id=action_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    action_id: UUID,
) -> Response[ErrorEnvelope | OutcomeView]:
    """Get an action's seven-day outcome window and result (OUT-001).

     Returns the OUT-001 seven-day outcome window for a reconciled action and, once the window has
    closed, its §15.3 result and confidence (or Not Measurable when the required evidence is absent).
    This is a read.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | OutcomeView]
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
) -> ErrorEnvelope | OutcomeView | None:
    """Get an action's seven-day outcome window and result (OUT-001).

     Returns the OUT-001 seven-day outcome window for a reconciled action and, once the window has
    closed, its §15.3 result and confidence (or Not Measurable when the required evidence is absent).
    This is a read.

    Args:
        action_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | OutcomeView
    """

    return (
        await asyncio_detailed(
            client=client,
            action_id=action_id,
        )
    ).parsed
