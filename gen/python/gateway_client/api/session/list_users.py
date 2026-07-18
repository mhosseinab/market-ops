from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.user_list import UserList
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/users",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient | Client, response: httpx.Response) -> ErrorEnvelope | UserList:
    if response.status_code == 200:
        response_200 = UserList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | UserList]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ErrorEnvelope | UserList]:
    """List the organization's user roster (PD-3 item 7).

     Returns every user in the caller's organization (id, email, role, created_at) — the roster the
    Settings screen needs beyond the current session principal (GET /auth/me). Reading is L1, every
    role; adding or changing a user's role stays the existing L3 guardrail.manage_users write path
    (unaffected by this read).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | UserList]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
) -> ErrorEnvelope | UserList | None:
    """List the organization's user roster (PD-3 item 7).

     Returns every user in the caller's organization (id, email, role, created_at) — the roster the
    Settings screen needs beyond the current session principal (GET /auth/me). Reading is L1, every
    role; adding or changing a user's role stays the existing L3 guardrail.manage_users write path
    (unaffected by this read).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | UserList
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
) -> Response[ErrorEnvelope | UserList]:
    """List the organization's user roster (PD-3 item 7).

     Returns every user in the caller's organization (id, email, role, created_at) — the roster the
    Settings screen needs beyond the current session principal (GET /auth/me). Reading is L1, every
    role; adding or changing a user's role stays the existing L3 guardrail.manage_users write path
    (unaffected by this read).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | UserList]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
) -> ErrorEnvelope | UserList | None:
    """List the organization's user roster (PD-3 item 7).

     Returns every user in the caller's organization (id, email, role, created_at) — the roster the
    Settings screen needs beyond the current session principal (GET /auth/me). Reading is L1, every
    role; adding or changing a user's role stays the existing L3 guardrail.manage_users write path
    (unaffected by this read).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | UserList
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
