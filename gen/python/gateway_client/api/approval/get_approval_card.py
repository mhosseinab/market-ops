from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.approval_card_view import ApprovalCardView
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response


def _get_kwargs(
    *,
    card_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_card_id = str(card_id)
    params["cardId"] = json_card_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/approvals/card",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApprovalCardView | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = ApprovalCardView.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApprovalCardView | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    card_id: UUID,
) -> Response[ApprovalCardView | ErrorEnvelope]:
    """Get an approval card and its append-only §8.4 state history.

     Returns one versioned approval card (PRD §7.5 APR-001) with its current §8.4 state and the APPEND-
    ONLY lifecycle history (AUD-001). The structured control's bound versions (action id,
    parameter/context/policy/cost versions, evidence versions, expiry) are surfaced so the surface can
    present the control and re-verify it on confirmation. This is a read; it never advances state.

    Args:
        card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApprovalCardView | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        card_id=card_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    card_id: UUID,
) -> ApprovalCardView | ErrorEnvelope | None:
    """Get an approval card and its append-only §8.4 state history.

     Returns one versioned approval card (PRD §7.5 APR-001) with its current §8.4 state and the APPEND-
    ONLY lifecycle history (AUD-001). The structured control's bound versions (action id,
    parameter/context/policy/cost versions, evidence versions, expiry) are surfaced so the surface can
    present the control and re-verify it on confirmation. This is a read; it never advances state.

    Args:
        card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApprovalCardView | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        card_id=card_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    card_id: UUID,
) -> Response[ApprovalCardView | ErrorEnvelope]:
    """Get an approval card and its append-only §8.4 state history.

     Returns one versioned approval card (PRD §7.5 APR-001) with its current §8.4 state and the APPEND-
    ONLY lifecycle history (AUD-001). The structured control's bound versions (action id,
    parameter/context/policy/cost versions, evidence versions, expiry) are surfaced so the surface can
    present the control and re-verify it on confirmation. This is a read; it never advances state.

    Args:
        card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApprovalCardView | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        card_id=card_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    card_id: UUID,
) -> ApprovalCardView | ErrorEnvelope | None:
    """Get an approval card and its append-only §8.4 state history.

     Returns one versioned approval card (PRD §7.5 APR-001) with its current §8.4 state and the APPEND-
    ONLY lifecycle history (AUD-001). The structured control's bound versions (action id,
    parameter/context/policy/cost versions, evidence versions, expiry) are surfaced so the surface can
    present the control and re-verify it on confirmation. This is a read; it never advances state.

    Args:
        card_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApprovalCardView | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            card_id=card_id,
        )
    ).parsed
