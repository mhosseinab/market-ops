from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.identity_decision_request import IdentityDecisionRequest
from ...models.market_product_identity import MarketProductIdentity
from ...types import Response


def _get_kwargs(
    *,
    body: IdentityDecisionRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/identity/defer",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | MarketProductIdentity:
    if response.status_code == 200:
        response_200 = MarketProductIdentity.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | MarketProductIdentity]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: IdentityDecisionRequest,
) -> Response[ErrorEnvelope | MarketProductIdentity]:
    """Defer a Needs Review candidate, keeping it in the queue.

     Leaves a NeedsReview candidate in the queue (journey 4 step 2) and records the deferral in the
    append-only audit. It never promotes the mapping to an executable state.

    Args:
        body (IdentityDecisionRequest): A confirm / reject / defer decision on a Needs Review
            candidate. The optional note is free text captured as audit evidence; it carries NO
            authority (PRD §8): the structured operation itself is the decision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarketProductIdentity]
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
    body: IdentityDecisionRequest,
) -> ErrorEnvelope | MarketProductIdentity | None:
    """Defer a Needs Review candidate, keeping it in the queue.

     Leaves a NeedsReview candidate in the queue (journey 4 step 2) and records the deferral in the
    append-only audit. It never promotes the mapping to an executable state.

    Args:
        body (IdentityDecisionRequest): A confirm / reject / defer decision on a Needs Review
            candidate. The optional note is free text captured as audit evidence; it carries NO
            authority (PRD §8): the structured operation itself is the decision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarketProductIdentity
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: IdentityDecisionRequest,
) -> Response[ErrorEnvelope | MarketProductIdentity]:
    """Defer a Needs Review candidate, keeping it in the queue.

     Leaves a NeedsReview candidate in the queue (journey 4 step 2) and records the deferral in the
    append-only audit. It never promotes the mapping to an executable state.

    Args:
        body (IdentityDecisionRequest): A confirm / reject / defer decision on a Needs Review
            candidate. The optional note is free text captured as audit evidence; it carries NO
            authority (PRD §8): the structured operation itself is the decision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | MarketProductIdentity]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: IdentityDecisionRequest,
) -> ErrorEnvelope | MarketProductIdentity | None:
    """Defer a Needs Review candidate, keeping it in the queue.

     Leaves a NeedsReview candidate in the queue (journey 4 step 2) and records the deferral in the
    append-only audit. It never promotes the mapping to an executable state.

    Args:
        body (IdentityDecisionRequest): A confirm / reject / defer decision on a Needs Review
            candidate. The optional note is free text captured as audit evidence; it carries NO
            authority (PRD §8): the structured operation itself is the decision.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | MarketProductIdentity
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
