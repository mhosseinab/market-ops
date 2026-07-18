from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.level_2_proposal_request import Level2ProposalRequest
from ...models.level_2_proposal_result import Level2ProposalResult
from ...types import Response


def _get_kwargs(
    *,
    body: Level2ProposalRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/chat/cards/level2-proposal",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | Level2ProposalResult:
    if response.status_code == 200:
        response_200 = Level2ProposalResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | Level2ProposalResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
    body: Level2ProposalRequest,
) -> Response[ErrorEnvelope | Level2ProposalResult]:
    """Create a Level-2 reversible-config before/after proposal (CHAT-061/062).

     The Draft-only write for a Level-2 (reversible configuration) proposal (PRD §8.3, CHAT-061/062). It
    authorizes against perm.GatewayCan(draft.level2_proposal). It writes the
    before/after/scope/consequence proposal AND an append-only audit row in ONE transaction (fail-closed
    on audit error) so the governance change is transcript-independently reproducible (AUD-001). NO
    Level-3 write path exists. TERMINAL AT DRAFT — confirmation is the screens' structured control.

    Args:
        body (Level2ProposalRequest): A Level-2 reversible-config proposal (CHAT-061/062):
            before/after catalog keys plus the setting being changed. Keys are locale-neutral catalog
            keys (LOC-001) — no copy in the core.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | Level2ProposalResult]
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
    body: Level2ProposalRequest,
) -> ErrorEnvelope | Level2ProposalResult | None:
    """Create a Level-2 reversible-config before/after proposal (CHAT-061/062).

     The Draft-only write for a Level-2 (reversible configuration) proposal (PRD §8.3, CHAT-061/062). It
    authorizes against perm.GatewayCan(draft.level2_proposal). It writes the
    before/after/scope/consequence proposal AND an append-only audit row in ONE transaction (fail-closed
    on audit error) so the governance change is transcript-independently reproducible (AUD-001). NO
    Level-3 write path exists. TERMINAL AT DRAFT — confirmation is the screens' structured control.

    Args:
        body (Level2ProposalRequest): A Level-2 reversible-config proposal (CHAT-061/062):
            before/after catalog keys plus the setting being changed. Keys are locale-neutral catalog
            keys (LOC-001) — no copy in the core.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | Level2ProposalResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
    body: Level2ProposalRequest,
) -> Response[ErrorEnvelope | Level2ProposalResult]:
    """Create a Level-2 reversible-config before/after proposal (CHAT-061/062).

     The Draft-only write for a Level-2 (reversible configuration) proposal (PRD §8.3, CHAT-061/062). It
    authorizes against perm.GatewayCan(draft.level2_proposal). It writes the
    before/after/scope/consequence proposal AND an append-only audit row in ONE transaction (fail-closed
    on audit error) so the governance change is transcript-independently reproducible (AUD-001). NO
    Level-3 write path exists. TERMINAL AT DRAFT — confirmation is the screens' structured control.

    Args:
        body (Level2ProposalRequest): A Level-2 reversible-config proposal (CHAT-061/062):
            before/after catalog keys plus the setting being changed. Keys are locale-neutral catalog
            keys (LOC-001) — no copy in the core.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | Level2ProposalResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
    body: Level2ProposalRequest,
) -> ErrorEnvelope | Level2ProposalResult | None:
    """Create a Level-2 reversible-config before/after proposal (CHAT-061/062).

     The Draft-only write for a Level-2 (reversible configuration) proposal (PRD §8.3, CHAT-061/062). It
    authorizes against perm.GatewayCan(draft.level2_proposal). It writes the
    before/after/scope/consequence proposal AND an append-only audit row in ONE transaction (fail-closed
    on audit error) so the governance change is transcript-independently reproducible (AUD-001). NO
    Level-3 write path exists. TERMINAL AT DRAFT — confirmation is the screens' structured control.

    Args:
        body (Level2ProposalRequest): A Level-2 reversible-config proposal (CHAT-061/062):
            before/after catalog keys plus the setting being changed. Keys are locale-neutral catalog
            keys (LOC-001) — no copy in the core.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | Level2ProposalResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
