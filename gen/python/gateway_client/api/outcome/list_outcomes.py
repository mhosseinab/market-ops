from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.outcome_list import OutcomeList
from ...types import UNSET, Response, Unset


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
    limit: int | Unset = UNSET,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    params["limit"] = limit

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/outcomes/list",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | OutcomeList:
    if response.status_code == 200:
        response_200 = OutcomeList.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | OutcomeList]:
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
    limit: int | Unset = UNSET,
) -> Response[ErrorEnvelope | OutcomeList]:
    """List an account's outcome windows and results (OUT-001, PD-3 item 5).

     Returns the account's OUT-001 outcome windows, newest first, with their §15.3 result and confidence
    once closed (absent while the window is still open — never a fabricated Not Measurable before it is
    actually computed). This is a read.

    Args:
        marketplace_account_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | OutcomeList]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
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
    limit: int | Unset = UNSET,
) -> ErrorEnvelope | OutcomeList | None:
    """List an account's outcome windows and results (OUT-001, PD-3 item 5).

     Returns the account's OUT-001 outcome windows, newest first, with their §15.3 result and confidence
    once closed (absent while the window is still open — never a fabricated Not Measurable before it is
    actually computed). This is a read.

    Args:
        marketplace_account_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | OutcomeList
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
        limit=limit,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
    limit: int | Unset = UNSET,
) -> Response[ErrorEnvelope | OutcomeList]:
    """List an account's outcome windows and results (OUT-001, PD-3 item 5).

     Returns the account's OUT-001 outcome windows, newest first, with their §15.3 result and confidence
    once closed (absent while the window is still open — never a fabricated Not Measurable before it is
    actually computed). This is a read.

    Args:
        marketplace_account_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | OutcomeList]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
        limit=limit,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
    limit: int | Unset = UNSET,
) -> ErrorEnvelope | OutcomeList | None:
    """List an account's outcome windows and results (OUT-001, PD-3 item 5).

     Returns the account's OUT-001 outcome windows, newest first, with their §15.3 result and confidence
    once closed (absent while the window is still open — never a fabricated Not Measurable before it is
    actually computed). This is a read.

    Args:
        marketplace_account_id (UUID):
        limit (int | Unset):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | OutcomeList
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
            limit=limit,
        )
    ).parsed
