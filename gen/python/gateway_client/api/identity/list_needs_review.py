from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.needs_review_queue import NeedsReviewQueue
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
        "url": "/identity/needs-review",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | NeedsReviewQueue:
    if response.status_code == 200:
        response_200 = NeedsReviewQueue.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | NeedsReviewQueue]:
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
) -> Response[ErrorEnvelope | NeedsReviewQueue]:
    """List the Needs Review identity-mapping queue for an account.

     Returns the account's pending Market Product Identity candidates (journey 4 step 1): each row
    carries SKU (supplier code), variant and product title, and the native-id evidence a reviewer needs.
    Only NeedsReview candidates appear here — Confirmed/Rejected/Obsolete mappings are never in the
    queue. Confirming a candidate is the ONLY thing that makes a variant an observation target (CAT-002,
    OBS-001).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | NeedsReviewQueue]
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
    client: Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | NeedsReviewQueue | None:
    """List the Needs Review identity-mapping queue for an account.

     Returns the account's pending Market Product Identity candidates (journey 4 step 1): each row
    carries SKU (supplier code), variant and product title, and the native-id evidence a reviewer needs.
    Only NeedsReview candidates appear here — Confirmed/Rejected/Obsolete mappings are never in the
    queue. Confirming a candidate is the ONLY thing that makes a variant an observation target (CAT-002,
    OBS-001).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | NeedsReviewQueue
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | NeedsReviewQueue]:
    """List the Needs Review identity-mapping queue for an account.

     Returns the account's pending Market Product Identity candidates (journey 4 step 1): each row
    carries SKU (supplier code), variant and product title, and the native-id evidence a reviewer needs.
    Only NeedsReview candidates appear here — Confirmed/Rejected/Obsolete mappings are never in the
    queue. Confirming a candidate is the ONLY thing that makes a variant an observation target (CAT-002,
    OBS-001).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | NeedsReviewQueue]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | NeedsReviewQueue | None:
    """List the Needs Review identity-mapping queue for an account.

     Returns the account's pending Market Product Identity candidates (journey 4 step 1): each row
    carries SKU (supplier code), variant and product title, and the native-id evidence a reviewer needs.
    Only NeedsReview candidates appear here — Confirmed/Rejected/Obsolete mappings are never in the
    queue. Confirming a candidate is the ONLY thing that makes a variant an observation target (CAT-002,
    OBS-001).

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | NeedsReviewQueue
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
