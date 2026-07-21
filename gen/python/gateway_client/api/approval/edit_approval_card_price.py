from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.approval_card_view import ApprovalCardView
from ...models.edit_approval_card_price_request import EditApprovalCardPriceRequest
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: EditApprovalCardPriceRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/approvals/card/edit-price",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ApprovalCardView | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = ApprovalCardView.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ApprovalCardView | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: EditApprovalCardPriceRequest,
) -> Response[ApprovalCardView | ErrorEnvelope]:
    """Edit an approval card's proposed price before confirmation (CHAT-044, PD-3 item 2).

     Mints a NEW card version with a NEW parameter version and a fresh control-eligible Draft state
    (approval.Card.EditPrice) — the price is NEVER mutated in place, and the prior control (if any) is
    thereby invalidated (its parameter version no longer matches). This is write-adjacent but
    reversible: L2 price.edit, Owner + Operator only. The read/Draft-only LLM machine credential can
    never reach this endpoint (§12.3) — it is deliberately neither an L1 read nor a Draft-only action.

    Args:
        body (EditApprovalCardPriceRequest): The CHAT-044 price edit request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApprovalCardView | ErrorEnvelope]
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
    body: EditApprovalCardPriceRequest,
) -> ApprovalCardView | ErrorEnvelope | None:
    """Edit an approval card's proposed price before confirmation (CHAT-044, PD-3 item 2).

     Mints a NEW card version with a NEW parameter version and a fresh control-eligible Draft state
    (approval.Card.EditPrice) — the price is NEVER mutated in place, and the prior control (if any) is
    thereby invalidated (its parameter version no longer matches). This is write-adjacent but
    reversible: L2 price.edit, Owner + Operator only. The read/Draft-only LLM machine credential can
    never reach this endpoint (§12.3) — it is deliberately neither an L1 read nor a Draft-only action.

    Args:
        body (EditApprovalCardPriceRequest): The CHAT-044 price edit request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApprovalCardView | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: EditApprovalCardPriceRequest,
) -> Response[ApprovalCardView | ErrorEnvelope]:
    """Edit an approval card's proposed price before confirmation (CHAT-044, PD-3 item 2).

     Mints a NEW card version with a NEW parameter version and a fresh control-eligible Draft state
    (approval.Card.EditPrice) — the price is NEVER mutated in place, and the prior control (if any) is
    thereby invalidated (its parameter version no longer matches). This is write-adjacent but
    reversible: L2 price.edit, Owner + Operator only. The read/Draft-only LLM machine credential can
    never reach this endpoint (§12.3) — it is deliberately neither an L1 read nor a Draft-only action.

    Args:
        body (EditApprovalCardPriceRequest): The CHAT-044 price edit request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApprovalCardView | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: EditApprovalCardPriceRequest,
) -> ApprovalCardView | ErrorEnvelope | None:
    """Edit an approval card's proposed price before confirmation (CHAT-044, PD-3 item 2).

     Mints a NEW card version with a NEW parameter version and a fresh control-eligible Draft state
    (approval.Card.EditPrice) — the price is NEVER mutated in place, and the prior control (if any) is
    thereby invalidated (its parameter version no longer matches). This is write-adjacent but
    reversible: L2 price.edit, Owner + Operator only. The read/Draft-only LLM machine credential can
    never reach this endpoint (§12.3) — it is deliberately neither an L1 read nor a Draft-only action.

    Args:
        body (EditApprovalCardPriceRequest): The CHAT-044 price edit request.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApprovalCardView | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
