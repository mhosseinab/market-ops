from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.selection_set_preview_request import SelectionSetPreviewRequest
from ...models.selection_set_preview_result import SelectionSetPreviewResult
from ...types import Response


def _get_kwargs(
    *,
    body: SelectionSetPreviewRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/selection-sets/preview",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | SelectionSetPreviewResult:
    if response.status_code == 200:
        response_200 = SelectionSetPreviewResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | SelectionSetPreviewResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: SelectionSetPreviewRequest,
) -> Response[ErrorEnvelope | SelectionSetPreviewResult]:
    r"""Build a bulk selection-set preview with a SERVER-MINTED version (PD-3 item 4).

     The screens-native bulk preview (as distinct from the chat draft.selection_set compile path). The
    request carries NO version field — the SERVER is the sole authority that mints the selection-set
    version (recommendation.CreateSelectionSet's append-only \"next version per lineage\" numbering); a
    client can never present or influence it. This is the hard S35/S37 safety precondition for bulk
    execution: any later bulk confirmation binds to EXACTLY this server-minted version, and any set
    change mints a new one, invalidating a stale bound confirmation (CHAT-051/052). Omitting `lineageId`
    starts a NEW lineage; supplying an existing one mints the next version within it (a refreshed
    preview). Each member's disposition is resolved SERVER-SIDE from its current, persisted
    recommendation — never taken from the client. L2 selection.bulk_preview, Owner + Operator only; the
    machine gateway credential can never reach it.

    Args:
        body (SelectionSetPreviewRequest): The screens-native bulk preview request (PD-3 item 4).
            Carries NO version field by construction — the server is the sole authority that mints the
            selection-set version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | SelectionSetPreviewResult]
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
    body: SelectionSetPreviewRequest,
) -> ErrorEnvelope | SelectionSetPreviewResult | None:
    r"""Build a bulk selection-set preview with a SERVER-MINTED version (PD-3 item 4).

     The screens-native bulk preview (as distinct from the chat draft.selection_set compile path). The
    request carries NO version field — the SERVER is the sole authority that mints the selection-set
    version (recommendation.CreateSelectionSet's append-only \"next version per lineage\" numbering); a
    client can never present or influence it. This is the hard S35/S37 safety precondition for bulk
    execution: any later bulk confirmation binds to EXACTLY this server-minted version, and any set
    change mints a new one, invalidating a stale bound confirmation (CHAT-051/052). Omitting `lineageId`
    starts a NEW lineage; supplying an existing one mints the next version within it (a refreshed
    preview). Each member's disposition is resolved SERVER-SIDE from its current, persisted
    recommendation — never taken from the client. L2 selection.bulk_preview, Owner + Operator only; the
    machine gateway credential can never reach it.

    Args:
        body (SelectionSetPreviewRequest): The screens-native bulk preview request (PD-3 item 4).
            Carries NO version field by construction — the server is the sole authority that mints the
            selection-set version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | SelectionSetPreviewResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: SelectionSetPreviewRequest,
) -> Response[ErrorEnvelope | SelectionSetPreviewResult]:
    r"""Build a bulk selection-set preview with a SERVER-MINTED version (PD-3 item 4).

     The screens-native bulk preview (as distinct from the chat draft.selection_set compile path). The
    request carries NO version field — the SERVER is the sole authority that mints the selection-set
    version (recommendation.CreateSelectionSet's append-only \"next version per lineage\" numbering); a
    client can never present or influence it. This is the hard S35/S37 safety precondition for bulk
    execution: any later bulk confirmation binds to EXACTLY this server-minted version, and any set
    change mints a new one, invalidating a stale bound confirmation (CHAT-051/052). Omitting `lineageId`
    starts a NEW lineage; supplying an existing one mints the next version within it (a refreshed
    preview). Each member's disposition is resolved SERVER-SIDE from its current, persisted
    recommendation — never taken from the client. L2 selection.bulk_preview, Owner + Operator only; the
    machine gateway credential can never reach it.

    Args:
        body (SelectionSetPreviewRequest): The screens-native bulk preview request (PD-3 item 4).
            Carries NO version field by construction — the server is the sole authority that mints the
            selection-set version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | SelectionSetPreviewResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: SelectionSetPreviewRequest,
) -> ErrorEnvelope | SelectionSetPreviewResult | None:
    r"""Build a bulk selection-set preview with a SERVER-MINTED version (PD-3 item 4).

     The screens-native bulk preview (as distinct from the chat draft.selection_set compile path). The
    request carries NO version field — the SERVER is the sole authority that mints the selection-set
    version (recommendation.CreateSelectionSet's append-only \"next version per lineage\" numbering); a
    client can never present or influence it. This is the hard S35/S37 safety precondition for bulk
    execution: any later bulk confirmation binds to EXACTLY this server-minted version, and any set
    change mints a new one, invalidating a stale bound confirmation (CHAT-051/052). Omitting `lineageId`
    starts a NEW lineage; supplying an existing one mints the next version within it (a refreshed
    preview). Each member's disposition is resolved SERVER-SIDE from its current, persisted
    recommendation — never taken from the client. L2 selection.bulk_preview, Owner + Operator only; the
    machine gateway credential can never reach it.

    Args:
        body (SelectionSetPreviewRequest): The screens-native bulk preview request (PD-3 item 4).
            Carries NO version field by construction — the server is the sole authority that mints the
            selection-set version.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | SelectionSetPreviewResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
