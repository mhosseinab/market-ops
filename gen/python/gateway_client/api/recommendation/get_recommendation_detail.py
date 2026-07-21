from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.recommendation_detail import RecommendationDetail
from ...types import UNSET, Response


def _get_kwargs(
    *,
    recommendation_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_recommendation_id = str(recommendation_id)
    params["recommendationId"] = json_recommendation_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/recommendations/detail",
        "params": params,
    }

    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | RecommendationDetail:
    if response.status_code == 200:
        response_200 = RecommendationDetail.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | RecommendationDetail]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    recommendation_id: UUID,
) -> Response[ErrorEnvelope | RecommendationDetail]:
    """Get one recommendation's full PRC-001 record + contribution breakdown (S37).

     Returns the complete PRC-001 record for a single, persisted recommendation version: objective,
    current/proposed price, the contribution breakdown (§9.2 deductions — present-or-unavailable-with-
    reason, never fabricated), the allowed range, evidence quality, readiness, assumptions, and blockers
    (PRC-002, in policy order). This is the consolidated read PD-3 items 1/3 (dk-p0-product-
    decisions.md) — the same PRC-001-complete payload the approval card is minted from. It is a read; it
    never advances state or mints a control.

    Args:
        recommendation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | RecommendationDetail]
    """

    kwargs = _get_kwargs(
        recommendation_id=recommendation_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: Client,
    recommendation_id: UUID,
) -> ErrorEnvelope | RecommendationDetail | None:
    """Get one recommendation's full PRC-001 record + contribution breakdown (S37).

     Returns the complete PRC-001 record for a single, persisted recommendation version: objective,
    current/proposed price, the contribution breakdown (§9.2 deductions — present-or-unavailable-with-
    reason, never fabricated), the allowed range, evidence quality, readiness, assumptions, and blockers
    (PRC-002, in policy order). This is the consolidated read PD-3 items 1/3 (dk-p0-product-
    decisions.md) — the same PRC-001-complete payload the approval card is minted from. It is a read; it
    never advances state or mints a control.

    Args:
        recommendation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | RecommendationDetail
    """

    return sync_detailed(
        client=client,
        recommendation_id=recommendation_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    recommendation_id: UUID,
) -> Response[ErrorEnvelope | RecommendationDetail]:
    """Get one recommendation's full PRC-001 record + contribution breakdown (S37).

     Returns the complete PRC-001 record for a single, persisted recommendation version: objective,
    current/proposed price, the contribution breakdown (§9.2 deductions — present-or-unavailable-with-
    reason, never fabricated), the allowed range, evidence quality, readiness, assumptions, and blockers
    (PRC-002, in policy order). This is the consolidated read PD-3 items 1/3 (dk-p0-product-
    decisions.md) — the same PRC-001-complete payload the approval card is minted from. It is a read; it
    never advances state or mints a control.

    Args:
        recommendation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | RecommendationDetail]
    """

    kwargs = _get_kwargs(
        recommendation_id=recommendation_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    recommendation_id: UUID,
) -> ErrorEnvelope | RecommendationDetail | None:
    """Get one recommendation's full PRC-001 record + contribution breakdown (S37).

     Returns the complete PRC-001 record for a single, persisted recommendation version: objective,
    current/proposed price, the contribution breakdown (§9.2 deductions — present-or-unavailable-with-
    reason, never fabricated), the allowed range, evidence quality, readiness, assumptions, and blockers
    (PRC-002, in policy order). This is the consolidated read PD-3 items 1/3 (dk-p0-product-
    decisions.md) — the same PRC-001-complete payload the approval card is minted from. It is a read; it
    never advances state or mints a control.

    Args:
        recommendation_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | RecommendationDetail
    """

    return (
        await asyncio_detailed(
            client=client,
            recommendation_id=recommendation_id,
        )
    ).parsed
