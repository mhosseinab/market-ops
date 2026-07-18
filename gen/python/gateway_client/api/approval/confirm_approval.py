from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.approval_confirm_request import ApprovalConfirmRequest
from ...models.approval_confirm_result import ApprovalConfirmResult
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: ApprovalConfirmRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/approvals/confirm",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ApprovalConfirmResult | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = ApprovalConfirmResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ApprovalConfirmResult | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApprovalConfirmRequest,
) -> Response[ApprovalConfirmResult | ErrorEnvelope]:
    """Activate the structured control on an individual approval card (APR-001).

     The ONLY individual approval path (§8, never-cut free-text containment): activates the structured,
    version-bound control on a card in AwaitingConfirmation. The request MUST carry the exact bound
    versions (action id, parameter/context/policy/cost versions, evidence versions); the server re-
    verifies EVERY one against the live card. ANY changed dimension routes to Invalidated and an elapsed
    expiry to Expired — only a fully-matching, live control reaches Approved (§8.4). Free text can never
    satisfy this contract. Execution itself (the Revalidating → Executing boundary) lands in S18 and is
    stubbed closed here: an Approved card reports `executionPending` true and performs no write.

    Args:
        body (ApprovalConfirmRequest): The structured individual-approval control activation (§8,
            APR-001). It MUST carry the exact bound versions; the server re-verifies every one against
            the live card. This is the only individual approval path — free text cannot satisfy it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApprovalConfirmResult | ErrorEnvelope]
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
    client: AuthenticatedClient | Client,
    body: ApprovalConfirmRequest,
) -> ApprovalConfirmResult | ErrorEnvelope | None:
    """Activate the structured control on an individual approval card (APR-001).

     The ONLY individual approval path (§8, never-cut free-text containment): activates the structured,
    version-bound control on a card in AwaitingConfirmation. The request MUST carry the exact bound
    versions (action id, parameter/context/policy/cost versions, evidence versions); the server re-
    verifies EVERY one against the live card. ANY changed dimension routes to Invalidated and an elapsed
    expiry to Expired — only a fully-matching, live control reaches Approved (§8.4). Free text can never
    satisfy this contract. Execution itself (the Revalidating → Executing boundary) lands in S18 and is
    stubbed closed here: an Approved card reports `executionPending` true and performs no write.

    Args:
        body (ApprovalConfirmRequest): The structured individual-approval control activation (§8,
            APR-001). It MUST carry the exact bound versions; the server re-verifies every one against
            the live card. This is the only individual approval path — free text cannot satisfy it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApprovalConfirmResult | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: ApprovalConfirmRequest,
) -> Response[ApprovalConfirmResult | ErrorEnvelope]:
    """Activate the structured control on an individual approval card (APR-001).

     The ONLY individual approval path (§8, never-cut free-text containment): activates the structured,
    version-bound control on a card in AwaitingConfirmation. The request MUST carry the exact bound
    versions (action id, parameter/context/policy/cost versions, evidence versions); the server re-
    verifies EVERY one against the live card. ANY changed dimension routes to Invalidated and an elapsed
    expiry to Expired — only a fully-matching, live control reaches Approved (§8.4). Free text can never
    satisfy this contract. Execution itself (the Revalidating → Executing boundary) lands in S18 and is
    stubbed closed here: an Approved card reports `executionPending` true and performs no write.

    Args:
        body (ApprovalConfirmRequest): The structured individual-approval control activation (§8,
            APR-001). It MUST carry the exact bound versions; the server re-verifies every one against
            the live card. This is the only individual approval path — free text cannot satisfy it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ApprovalConfirmResult | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: ApprovalConfirmRequest,
) -> ApprovalConfirmResult | ErrorEnvelope | None:
    """Activate the structured control on an individual approval card (APR-001).

     The ONLY individual approval path (§8, never-cut free-text containment): activates the structured,
    version-bound control on a card in AwaitingConfirmation. The request MUST carry the exact bound
    versions (action id, parameter/context/policy/cost versions, evidence versions); the server re-
    verifies EVERY one against the live card. ANY changed dimension routes to Invalidated and an elapsed
    expiry to Expired — only a fully-matching, live control reaches Approved (§8.4). Free text can never
    satisfy this contract. Execution itself (the Revalidating → Executing boundary) lands in S18 and is
    stubbed closed here: an Approved card reports `executionPending` true and performs no write.

    Args:
        body (ApprovalConfirmRequest): The structured individual-approval control activation (§8,
            APR-001). It MUST carry the exact bound versions; the server re-verifies every one against
            the live card. This is the only individual approval path — free text cannot satisfy it.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ApprovalConfirmResult | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
