from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.cost_import_preview import CostImportPreview
from ...models.error_envelope import ErrorEnvelope
from ...types import UNSET, Response


def _get_kwargs(
    *,
    batch_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_batch_id = str(batch_id)
    params["batchId"] = json_batch_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/cost/import",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CostImportPreview | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = CostImportPreview.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CostImportPreview | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    batch_id: UUID,
) -> Response[CostImportPreview | ErrorEnvelope]:
    """Re-fetch a stored cost-import preview batch.

     Returns a previously created preview batch and its disposition rows (CST-001), e.g. to restore the
    preview after a reload before confirmation.

    Args:
        batch_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostImportPreview | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        batch_id=batch_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    batch_id: UUID,
) -> CostImportPreview | ErrorEnvelope | None:
    """Re-fetch a stored cost-import preview batch.

     Returns a previously created preview batch and its disposition rows (CST-001), e.g. to restore the
    preview after a reload before confirmation.

    Args:
        batch_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostImportPreview | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        batch_id=batch_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    batch_id: UUID,
) -> Response[CostImportPreview | ErrorEnvelope]:
    """Re-fetch a stored cost-import preview batch.

     Returns a previously created preview batch and its disposition rows (CST-001), e.g. to restore the
    preview after a reload before confirmation.

    Args:
        batch_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CostImportPreview | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        batch_id=batch_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    batch_id: UUID,
) -> CostImportPreview | ErrorEnvelope | None:
    """Re-fetch a stored cost-import preview batch.

     Returns a previously created preview batch and its disposition rows (CST-001), e.g. to restore the
    preview after a reload before confirmation.

    Args:
        batch_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CostImportPreview | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            batch_id=batch_id,
        )
    ).parsed
