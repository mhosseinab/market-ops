from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.cost_import_preview import CostImportPreview
from ...models.cost_import_preview_request import CostImportPreviewRequest
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: CostImportPreviewRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/cost/import/preview",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> CostImportPreview | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CostImportPreview.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[CostImportPreview | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: CostImportPreviewRequest,
) -> Response[CostImportPreview | ErrorEnvelope]:
    """Build a CSV cost-import preview (no commit).

     Parses a UTF-8 cost CSV (Persian/Latin digits normalized, LOC-007), resolves SKUs, and returns a
    per-row disposition preview (CST-001). NO cost value is committed here — the returned batch is in
    'preview' and must be confirmed via /cost/import/commit. Every non-accept row carries a stated
    reason; duplicate (SKU, component) rows are a 'duplicate' conflict that blocks commit until resolved
    (§16).

    Args:
        body (CostImportPreviewRequest): Request to build a CSV cost-import preview. `csv` is the
            UTF-8 file content. An explicit column mapping is optional; when omitted the columns are
            auto-detected from the header row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostImportPreview | ErrorEnvelope]
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
    body: CostImportPreviewRequest,
) -> CostImportPreview | ErrorEnvelope | None:
    """Build a CSV cost-import preview (no commit).

     Parses a UTF-8 cost CSV (Persian/Latin digits normalized, LOC-007), resolves SKUs, and returns a
    per-row disposition preview (CST-001). NO cost value is committed here — the returned batch is in
    'preview' and must be confirmed via /cost/import/commit. Every non-accept row carries a stated
    reason; duplicate (SKU, component) rows are a 'duplicate' conflict that blocks commit until resolved
    (§16).

    Args:
        body (CostImportPreviewRequest): Request to build a CSV cost-import preview. `csv` is the
            UTF-8 file content. An explicit column mapping is optional; when omitted the columns are
            auto-detected from the header row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostImportPreview | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: CostImportPreviewRequest,
) -> Response[CostImportPreview | ErrorEnvelope]:
    """Build a CSV cost-import preview (no commit).

     Parses a UTF-8 cost CSV (Persian/Latin digits normalized, LOC-007), resolves SKUs, and returns a
    per-row disposition preview (CST-001). NO cost value is committed here — the returned batch is in
    'preview' and must be confirmed via /cost/import/commit. Every non-accept row carries a stated
    reason; duplicate (SKU, component) rows are a 'duplicate' conflict that blocks commit until resolved
    (§16).

    Args:
        body (CostImportPreviewRequest): Request to build a CSV cost-import preview. `csv` is the
            UTF-8 file content. An explicit column mapping is optional; when omitted the columns are
            auto-detected from the header row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostImportPreview | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: CostImportPreviewRequest,
) -> CostImportPreview | ErrorEnvelope | None:
    """Build a CSV cost-import preview (no commit).

     Parses a UTF-8 cost CSV (Persian/Latin digits normalized, LOC-007), resolves SKUs, and returns a
    per-row disposition preview (CST-001). NO cost value is committed here — the returned batch is in
    'preview' and must be confirmed via /cost/import/commit. Every non-accept row carries a stated
    reason; duplicate (SKU, component) rows are a 'duplicate' conflict that blocks commit until resolved
    (§16).

    Args:
        body (CostImportPreviewRequest): Request to build a CSV cost-import preview. `csv` is the
            UTF-8 file content. An explicit column mapping is optional; when omitted the columns are
            auto-detected from the header row.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostImportPreview | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
