from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.operations_queues import OperationsQueues
from ...types import UNSET, Response


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/ops/queues",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | OperationsQueues:
    if response.status_code == 200:
        response_200 = OperationsQueues.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | OperationsQueues]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | OperationsQueues]:
    """Aggregated Operations screen queues (PD-3 item 8).

     Returns the Operations screen's aggregated queues: pending-reconciliation actions (EXE-003, an
    unresolved external write awaiting resolution — never retried, never inferred). The parser/schema-
    drift queue (§10.4 Route C parser-drift events) is NOT YET backed by a persisted store; it reports
    `available: false` with an explicit reason rather than a fabricated empty success — closing it is a
    named follow-up on the Route C observer plane (go_connector_observer), not improvised here. Off-
    ladder operational read: Owner + Internal only, never Operator, never the machine gateway principal.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | OperationsQueues]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | OperationsQueues | None:
    """Aggregated Operations screen queues (PD-3 item 8).

     Returns the Operations screen's aggregated queues: pending-reconciliation actions (EXE-003, an
    unresolved external write awaiting resolution — never retried, never inferred). The parser/schema-
    drift queue (§10.4 Route C parser-drift events) is NOT YET backed by a persisted store; it reports
    `available: false` with an explicit reason rather than a fabricated empty success — closing it is a
    named follow-up on the Route C observer plane (go_connector_observer), not improvised here. Off-
    ladder operational read: Owner + Internal only, never Operator, never the machine gateway principal.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | OperationsQueues
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | OperationsQueues]:
    """Aggregated Operations screen queues (PD-3 item 8).

     Returns the Operations screen's aggregated queues: pending-reconciliation actions (EXE-003, an
    unresolved external write awaiting resolution — never retried, never inferred). The parser/schema-
    drift queue (§10.4 Route C parser-drift events) is NOT YET backed by a persisted store; it reports
    `available: false` with an explicit reason rather than a fabricated empty success — closing it is a
    named follow-up on the Route C observer plane (go_connector_observer), not improvised here. Off-
    ladder operational read: Owner + Internal only, never Operator, never the machine gateway principal.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | OperationsQueues]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | OperationsQueues | None:
    """Aggregated Operations screen queues (PD-3 item 8).

     Returns the Operations screen's aggregated queues: pending-reconciliation actions (EXE-003, an
    unresolved external write awaiting resolution — never retried, never inferred). The parser/schema-
    drift queue (§10.4 Route C parser-drift events) is NOT YET backed by a persisted store; it reports
    `available: false` with an explicit reason rather than a fabricated empty success — closing it is a
    named follow-up on the Route C observer plane (go_connector_observer), not improvised here. Off-
    ladder operational read: Owner + Internal only, never Operator, never the machine gateway principal.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | OperationsQueues
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
