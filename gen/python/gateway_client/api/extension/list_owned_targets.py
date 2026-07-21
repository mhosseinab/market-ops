from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient
from ...models.error_envelope import ErrorEnvelope
from ...models.observation_target_list import ObservationTargetList
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/ext/owned-targets",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient, response: httpx.Response) -> ErrorEnvelope | ObservationTargetList:
    if response.status_code == 200:
        response_200 = ObservationTargetList.from_dict(response.json())

        return response_200

    if response.status_code == 401:
        response_401 = ErrorEnvelope.from_dict(response.json())

        return response_401

    if response.status_code == 503:
        response_503 = ErrorEnvelope.from_dict(response.json())

        return response_503

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient, response: httpx.Response
) -> Response[ErrorEnvelope | ObservationTargetList]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
) -> Response[ErrorEnvelope | ObservationTargetList]:
    """List the paired account's Confirmed owned observation targets (EXT-004).

     The credential-scoped owned-target READ the browser extension calls to learn WHICH variants are the
    account's Confirmed owned targets (PRD §14 EXT-004, OBS-001). The marketplace account is derived
    SOLELY from the presented capture credential (captureAuth) — there is NO query or path parameter, so
    an extension can never select another seller's account (tenant authority is credential-derived,
    never caller-supplied). The server returns the SAME active targets as GET /observation/targets for
    that account (a target exists ONLY for an active Confirmed identity — a
    NeedsReview/Rejected/Obsolete/deactivated identity is never returned), so the extension's local
    owned-target projection can never resolve an unconfirmed product into the commercial path. A
    revoked/expired/unknown credential fails closed with 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ObservationTargetList]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
) -> ErrorEnvelope | ObservationTargetList | None:
    """List the paired account's Confirmed owned observation targets (EXT-004).

     The credential-scoped owned-target READ the browser extension calls to learn WHICH variants are the
    account's Confirmed owned targets (PRD §14 EXT-004, OBS-001). The marketplace account is derived
    SOLELY from the presented capture credential (captureAuth) — there is NO query or path parameter, so
    an extension can never select another seller's account (tenant authority is credential-derived,
    never caller-supplied). The server returns the SAME active targets as GET /observation/targets for
    that account (a target exists ONLY for an active Confirmed identity — a
    NeedsReview/Rejected/Obsolete/deactivated identity is never returned), so the extension's local
    owned-target projection can never resolve an unconfirmed product into the commercial path. A
    revoked/expired/unknown credential fails closed with 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ObservationTargetList
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
) -> Response[ErrorEnvelope | ObservationTargetList]:
    """List the paired account's Confirmed owned observation targets (EXT-004).

     The credential-scoped owned-target READ the browser extension calls to learn WHICH variants are the
    account's Confirmed owned targets (PRD §14 EXT-004, OBS-001). The marketplace account is derived
    SOLELY from the presented capture credential (captureAuth) — there is NO query or path parameter, so
    an extension can never select another seller's account (tenant authority is credential-derived,
    never caller-supplied). The server returns the SAME active targets as GET /observation/targets for
    that account (a target exists ONLY for an active Confirmed identity — a
    NeedsReview/Rejected/Obsolete/deactivated identity is never returned), so the extension's local
    owned-target projection can never resolve an unconfirmed product into the commercial path. A
    revoked/expired/unknown credential fails closed with 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | ObservationTargetList]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
) -> ErrorEnvelope | ObservationTargetList | None:
    """List the paired account's Confirmed owned observation targets (EXT-004).

     The credential-scoped owned-target READ the browser extension calls to learn WHICH variants are the
    account's Confirmed owned targets (PRD §14 EXT-004, OBS-001). The marketplace account is derived
    SOLELY from the presented capture credential (captureAuth) — there is NO query or path parameter, so
    an extension can never select another seller's account (tenant authority is credential-derived,
    never caller-supplied). The server returns the SAME active targets as GET /observation/targets for
    that account (a target exists ONLY for an active Confirmed identity — a
    NeedsReview/Rejected/Obsolete/deactivated identity is never returned), so the extension's local
    owned-target projection can never resolve an unconfirmed product into the commercial path. A
    revoked/expired/unknown credential fails closed with 401.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | ObservationTargetList
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
