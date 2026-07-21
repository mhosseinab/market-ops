from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.action_list import ActionList
from ...models.approval_state import ApprovalState
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
    state: ApprovalState | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    json_state: str | Unset = UNSET
    if not isinstance(state, Unset):
        json_state = state.value

    params["state"] = json_state

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/actions",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ActionList | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = ActionList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ActionList | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    state: ApprovalState | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[ActionList | ErrorEnvelope]:
    """List an account's actions (approval cards) as a grouped queue (PD-3 item 5).

     Returns the account's approval cards (one row per action, current version), newest first, optionally
    filtered by §8.4 state — the grouped multi-row queue the Actions screen needs beyond the single
    deep-linked card read (GET /approvals/card). This is a read; it never advances state.

    Args:
        marketplace_account_id (UUID):
        state (ApprovalState | Unset): One node of the §8.4 approval state machine. The set is
            closed; it is the authoritative lifecycle vocabulary for a card and its history.
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ActionList | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        state=state,
        limit=limit,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    marketplace_account_id: UUID,
    state: ApprovalState | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> ActionList | ErrorEnvelope | None:
    """List an account's actions (approval cards) as a grouped queue (PD-3 item 5).

     Returns the account's approval cards (one row per action, current version), newest first, optionally
    filtered by §8.4 state — the grouped multi-row queue the Actions screen needs beyond the single
    deep-linked card read (GET /approvals/card). This is a read; it never advances state.

    Args:
        marketplace_account_id (UUID):
        state (ApprovalState | Unset): One node of the §8.4 approval state machine. The set is
            closed; it is the authoritative lifecycle vocabulary for a card and its history.
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ActionList | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        state=state,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    state: ApprovalState | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> Response[ActionList | ErrorEnvelope]:
    """List an account's actions (approval cards) as a grouped queue (PD-3 item 5).

     Returns the account's approval cards (one row per action, current version), newest first, optionally
    filtered by §8.4 state — the grouped multi-row queue the Actions screen needs beyond the single
    deep-linked card read (GET /approvals/card). This is a read; it never advances state.

    Args:
        marketplace_account_id (UUID):
        state (ApprovalState | Unset): One node of the §8.4 approval state machine. The set is
            closed; it is the authoritative lifecycle vocabulary for a card and its history.
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ActionList | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        state=state,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
    state: ApprovalState | Unset = UNSET,
    limit: int | Unset = UNSET,
) -> ActionList | ErrorEnvelope | None:
    """List an account's actions (approval cards) as a grouped queue (PD-3 item 5).

     Returns the account's approval cards (one row per action, current version), newest first, optionally
    filtered by §8.4 state — the grouped multi-row queue the Actions screen needs beyond the single
    deep-linked card read (GET /approvals/card). This is a read; it never advances state.

    Args:
        marketplace_account_id (UUID):
        state (ApprovalState | Unset): One node of the §8.4 approval state machine. The set is
            closed; it is the authoritative lifecycle vocabulary for a card and its history.
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ActionList | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            state=state,
            limit=limit,
        )
    ).parsed
