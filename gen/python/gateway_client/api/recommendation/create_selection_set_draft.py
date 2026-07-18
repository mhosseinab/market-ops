from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.selection_set_draft_request import SelectionSetDraftRequest
from ...models.selection_set_draft_result import SelectionSetDraftResult
from ...types import Response


def _get_kwargs(
    *,
    body: SelectionSetDraftRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/chat/cards/selection-set-draft",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | SelectionSetDraftResult:
    if response.status_code == 200:
        response_200 = SelectionSetDraftResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | SelectionSetDraftResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: SelectionSetDraftRequest,
) -> Response[ErrorEnvelope | SelectionSetDraftResult]:
    """Create a named, versioned bulk selection-set Draft (CHAT-050/051).

     The Draft-only write for a bulk hand-off (PRD §12.1, CHAT-050/051). It authorizes against
    perm.GatewayCan(draft.selection_set). It compiles the conversational query into deterministic
    criteria and appends a NEW selection-set version, returning its bound versions + expiry. There is NO
    chat bulk approval — the confirmation binds ONE version through the screens' structured control.
    TERMINAL AT DRAFT.

    Args:
        body (SelectionSetDraftRequest): A bulk hand-off (CHAT-050/051): compile the
            conversational query into a named, versioned selection set. There is NO chat bulk
            approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | SelectionSetDraftResult]
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
    body: SelectionSetDraftRequest,
) -> ErrorEnvelope | SelectionSetDraftResult | None:
    """Create a named, versioned bulk selection-set Draft (CHAT-050/051).

     The Draft-only write for a bulk hand-off (PRD §12.1, CHAT-050/051). It authorizes against
    perm.GatewayCan(draft.selection_set). It compiles the conversational query into deterministic
    criteria and appends a NEW selection-set version, returning its bound versions + expiry. There is NO
    chat bulk approval — the confirmation binds ONE version through the screens' structured control.
    TERMINAL AT DRAFT.

    Args:
        body (SelectionSetDraftRequest): A bulk hand-off (CHAT-050/051): compile the
            conversational query into a named, versioned selection set. There is NO chat bulk
            approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | SelectionSetDraftResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: SelectionSetDraftRequest,
) -> Response[ErrorEnvelope | SelectionSetDraftResult]:
    """Create a named, versioned bulk selection-set Draft (CHAT-050/051).

     The Draft-only write for a bulk hand-off (PRD §12.1, CHAT-050/051). It authorizes against
    perm.GatewayCan(draft.selection_set). It compiles the conversational query into deterministic
    criteria and appends a NEW selection-set version, returning its bound versions + expiry. There is NO
    chat bulk approval — the confirmation binds ONE version through the screens' structured control.
    TERMINAL AT DRAFT.

    Args:
        body (SelectionSetDraftRequest): A bulk hand-off (CHAT-050/051): compile the
            conversational query into a named, versioned selection set. There is NO chat bulk
            approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | SelectionSetDraftResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: SelectionSetDraftRequest,
) -> ErrorEnvelope | SelectionSetDraftResult | None:
    """Create a named, versioned bulk selection-set Draft (CHAT-050/051).

     The Draft-only write for a bulk hand-off (PRD §12.1, CHAT-050/051). It authorizes against
    perm.GatewayCan(draft.selection_set). It compiles the conversational query into deterministic
    criteria and appends a NEW selection-set version, returning its bound versions + expiry. There is NO
    chat bulk approval — the confirmation binds ONE version through the screens' structured control.
    TERMINAL AT DRAFT.

    Args:
        body (SelectionSetDraftRequest): A bulk hand-off (CHAT-050/051): compile the
            conversational query into a named, versioned selection set. There is NO chat bulk
            approval.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | SelectionSetDraftResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
