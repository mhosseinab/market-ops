from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.cost_import_commit_request import CostImportCommitRequest
from ...models.cost_import_commit_result import CostImportCommitResult
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: CostImportCommitRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/cost/import/commit",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> CostImportCommitResult | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CostImportCommitResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[CostImportCommitResult | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: CostImportCommitRequest,
) -> Response[CostImportCommitResult | ErrorEnvelope]:
    """Commit a confirmed cost-import preview.

     Commits the ACCEPTED rows of a preview batch into append-only, component-versioned cost profiles
    (CST-001/CST-002) and recomputes margin readiness for every affected SKU. A batch that is not in
    'preview', or that still has duplicate conflicts, is refused (§16 no-commit-until- resolved).

    Args:
        body (CostImportCommitRequest): Confirm and commit a preview batch (CST-001).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostImportCommitResult | ErrorEnvelope]
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
    body: CostImportCommitRequest,
) -> CostImportCommitResult | ErrorEnvelope | None:
    """Commit a confirmed cost-import preview.

     Commits the ACCEPTED rows of a preview batch into append-only, component-versioned cost profiles
    (CST-001/CST-002) and recomputes margin readiness for every affected SKU. A batch that is not in
    'preview', or that still has duplicate conflicts, is refused (§16 no-commit-until- resolved).

    Args:
        body (CostImportCommitRequest): Confirm and commit a preview batch (CST-001).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostImportCommitResult | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: CostImportCommitRequest,
) -> Response[CostImportCommitResult | ErrorEnvelope]:
    """Commit a confirmed cost-import preview.

     Commits the ACCEPTED rows of a preview batch into append-only, component-versioned cost profiles
    (CST-001/CST-002) and recomputes margin readiness for every affected SKU. A batch that is not in
    'preview', or that still has duplicate conflicts, is refused (§16 no-commit-until- resolved).

    Args:
        body (CostImportCommitRequest): Confirm and commit a preview batch (CST-001).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostImportCommitResult | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: CostImportCommitRequest,
) -> CostImportCommitResult | ErrorEnvelope | None:
    """Commit a confirmed cost-import preview.

     Commits the ACCEPTED rows of a preview batch into append-only, component-versioned cost profiles
    (CST-001/CST-002) and recomputes margin readiness for every affected SKU. A batch that is not in
    'preview', or that still has duplicate conflicts, is refused (§16 no-commit-until- resolved).

    Args:
        body (CostImportCommitRequest): Confirm and commit a preview batch (CST-001).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostImportCommitResult | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
