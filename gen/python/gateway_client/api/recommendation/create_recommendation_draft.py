from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.recommendation_draft_request import RecommendationDraftRequest
from ...models.recommendation_draft_result import RecommendationDraftResult
from ...types import Response


def _get_kwargs(
    *,
    body: RecommendationDraftRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/chat/cards/recommendation-draft",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | RecommendationDraftResult:
    if response.status_code == 200:
        response_200 = RecommendationDraftResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | RecommendationDraftResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: RecommendationDraftRequest,
) -> Response[ErrorEnvelope | RecommendationDraftResult]:
    """Create an individual-approval Draft from a recommendation (CHAT-041).

     The Draft-only write the LLM/machine plane originates for a PrepareAction turn (PRD §8.2, §12.1,
    CHAT-041). It authorizes against the read/Draft-only gateway credential
    (perm.GatewayCan(draft.recommendation)) — a human session or any principal without the Draft
    capability is refused. It mints the initial §8.4 Draft approval card from the persisted, approvable
    recommendation and returns its bound versions + expiry so the gateway can render the card. The write
    is TERMINAL AT DRAFT: it never advances the state machine and never mints an approval control
    (confirmation happens through the same structured control endpoint as screens — chat never owns a
    confirm path). Fails closed (404) on an unknown/foreign/non-executable recommendation — never a
    fabricated Draft.

    Args:
        body (RecommendationDraftRequest): A PrepareAction hand-off (CHAT-041): create the
            individual-approval Draft for one persisted, approvable recommendation. All identifiers
            are snake_case to match the LLM plane's Draft-only transport contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | RecommendationDraftResult]
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
    client: AuthenticatedClient,
    body: RecommendationDraftRequest,
) -> ErrorEnvelope | RecommendationDraftResult | None:
    """Create an individual-approval Draft from a recommendation (CHAT-041).

     The Draft-only write the LLM/machine plane originates for a PrepareAction turn (PRD §8.2, §12.1,
    CHAT-041). It authorizes against the read/Draft-only gateway credential
    (perm.GatewayCan(draft.recommendation)) — a human session or any principal without the Draft
    capability is refused. It mints the initial §8.4 Draft approval card from the persisted, approvable
    recommendation and returns its bound versions + expiry so the gateway can render the card. The write
    is TERMINAL AT DRAFT: it never advances the state machine and never mints an approval control
    (confirmation happens through the same structured control endpoint as screens — chat never owns a
    confirm path). Fails closed (404) on an unknown/foreign/non-executable recommendation — never a
    fabricated Draft.

    Args:
        body (RecommendationDraftRequest): A PrepareAction hand-off (CHAT-041): create the
            individual-approval Draft for one persisted, approvable recommendation. All identifiers
            are snake_case to match the LLM plane's Draft-only transport contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | RecommendationDraftResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: RecommendationDraftRequest,
) -> Response[ErrorEnvelope | RecommendationDraftResult]:
    """Create an individual-approval Draft from a recommendation (CHAT-041).

     The Draft-only write the LLM/machine plane originates for a PrepareAction turn (PRD §8.2, §12.1,
    CHAT-041). It authorizes against the read/Draft-only gateway credential
    (perm.GatewayCan(draft.recommendation)) — a human session or any principal without the Draft
    capability is refused. It mints the initial §8.4 Draft approval card from the persisted, approvable
    recommendation and returns its bound versions + expiry so the gateway can render the card. The write
    is TERMINAL AT DRAFT: it never advances the state machine and never mints an approval control
    (confirmation happens through the same structured control endpoint as screens — chat never owns a
    confirm path). Fails closed (404) on an unknown/foreign/non-executable recommendation — never a
    fabricated Draft.

    Args:
        body (RecommendationDraftRequest): A PrepareAction hand-off (CHAT-041): create the
            individual-approval Draft for one persisted, approvable recommendation. All identifiers
            are snake_case to match the LLM plane's Draft-only transport contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | RecommendationDraftResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: RecommendationDraftRequest,
) -> ErrorEnvelope | RecommendationDraftResult | None:
    """Create an individual-approval Draft from a recommendation (CHAT-041).

     The Draft-only write the LLM/machine plane originates for a PrepareAction turn (PRD §8.2, §12.1,
    CHAT-041). It authorizes against the read/Draft-only gateway credential
    (perm.GatewayCan(draft.recommendation)) — a human session or any principal without the Draft
    capability is refused. It mints the initial §8.4 Draft approval card from the persisted, approvable
    recommendation and returns its bound versions + expiry so the gateway can render the card. The write
    is TERMINAL AT DRAFT: it never advances the state machine and never mints an approval control
    (confirmation happens through the same structured control endpoint as screens — chat never owns a
    confirm path). Fails closed (404) on an unknown/foreign/non-executable recommendation — never a
    fabricated Draft.

    Args:
        body (RecommendationDraftRequest): A PrepareAction hand-off (CHAT-041): create the
            individual-approval Draft for one persisted, approvable recommendation. All identifiers
            are snake_case to match the LLM plane's Draft-only transport contract.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | RecommendationDraftResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
