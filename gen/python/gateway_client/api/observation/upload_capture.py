from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.capture_accepted import CaptureAccepted
from ...models.capture_upload import CaptureUpload
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: CaptureUpload,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/observation/capture",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> CaptureAccepted | ErrorEnvelope:
    if response.status_code == 202:
        response_202 = CaptureAccepted.from_dict(response.json())

        return response_202

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[CaptureAccepted | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CaptureUpload,
) -> Response[CaptureAccepted | ErrorEnvelope]:
    """Upload an extension (Route B) observation capture.

     The server-side ingestion contract the Chrome extension calls into (PRD §10.1 Route B —
    corroboration / opportunistic refresh only). The request schema is ALLOW-LISTED
    (additionalProperties false): the extension may submit only Route B captures with the permitted
    fields, and may NOT self-certify schema/identity validity, conflict, or forge Route C freshness —
    those are server-side determinations. Price is raw evidence only (money quarantine); a capture never
    carries a Money. An equivalent replay is deduplicated (OBS-008): it creates no duplicate current
    offer and retains route provenance. Authenticated by the extension's scoped capture credential
    (captureAuth) obtained through pairing (EXT-001); a human session cookie is also accepted for first-
    party tooling. The capture credential is NEVER a seller-API token.

    Args:
        body (CaptureUpload): ALLOW-LISTED extension (Route B) capture upload (PRD §10.1). Only
            these fields are accepted (additionalProperties false). The extension cannot assert
            schema/identity validity or conflict, cannot forge Route C, and cannot declare a permanent
            disappearance — those are server-side. Price is raw evidence only (money quarantine).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CaptureAccepted | ErrorEnvelope]
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
    body: CaptureUpload,
) -> CaptureAccepted | ErrorEnvelope | None:
    """Upload an extension (Route B) observation capture.

     The server-side ingestion contract the Chrome extension calls into (PRD §10.1 Route B —
    corroboration / opportunistic refresh only). The request schema is ALLOW-LISTED
    (additionalProperties false): the extension may submit only Route B captures with the permitted
    fields, and may NOT self-certify schema/identity validity, conflict, or forge Route C freshness —
    those are server-side determinations. Price is raw evidence only (money quarantine); a capture never
    carries a Money. An equivalent replay is deduplicated (OBS-008): it creates no duplicate current
    offer and retains route provenance. Authenticated by the extension's scoped capture credential
    (captureAuth) obtained through pairing (EXT-001); a human session cookie is also accepted for first-
    party tooling. The capture credential is NEVER a seller-API token.

    Args:
        body (CaptureUpload): ALLOW-LISTED extension (Route B) capture upload (PRD §10.1). Only
            these fields are accepted (additionalProperties false). The extension cannot assert
            schema/identity validity or conflict, cannot forge Route C, and cannot declare a permanent
            disappearance — those are server-side. Price is raw evidence only (money quarantine).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CaptureAccepted | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: CaptureUpload,
) -> Response[CaptureAccepted | ErrorEnvelope]:
    """Upload an extension (Route B) observation capture.

     The server-side ingestion contract the Chrome extension calls into (PRD §10.1 Route B —
    corroboration / opportunistic refresh only). The request schema is ALLOW-LISTED
    (additionalProperties false): the extension may submit only Route B captures with the permitted
    fields, and may NOT self-certify schema/identity validity, conflict, or forge Route C freshness —
    those are server-side determinations. Price is raw evidence only (money quarantine); a capture never
    carries a Money. An equivalent replay is deduplicated (OBS-008): it creates no duplicate current
    offer and retains route provenance. Authenticated by the extension's scoped capture credential
    (captureAuth) obtained through pairing (EXT-001); a human session cookie is also accepted for first-
    party tooling. The capture credential is NEVER a seller-API token.

    Args:
        body (CaptureUpload): ALLOW-LISTED extension (Route B) capture upload (PRD §10.1). Only
            these fields are accepted (additionalProperties false). The extension cannot assert
            schema/identity validity or conflict, cannot forge Route C, and cannot declare a permanent
            disappearance — those are server-side. Price is raw evidence only (money quarantine).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[CaptureAccepted | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: CaptureUpload,
) -> CaptureAccepted | ErrorEnvelope | None:
    """Upload an extension (Route B) observation capture.

     The server-side ingestion contract the Chrome extension calls into (PRD §10.1 Route B —
    corroboration / opportunistic refresh only). The request schema is ALLOW-LISTED
    (additionalProperties false): the extension may submit only Route B captures with the permitted
    fields, and may NOT self-certify schema/identity validity, conflict, or forge Route C freshness —
    those are server-side determinations. Price is raw evidence only (money quarantine); a capture never
    carries a Money. An equivalent replay is deduplicated (OBS-008): it creates no duplicate current
    offer and retains route provenance. Authenticated by the extension's scoped capture credential
    (captureAuth) obtained through pairing (EXT-001); a human session cookie is also accepted for first-
    party tooling. The capture credential is NEVER a seller-API token.

    Args:
        body (CaptureUpload): ALLOW-LISTED extension (Route B) capture upload (PRD §10.1). Only
            these fields are accepted (additionalProperties false). The extension cannot assert
            schema/identity validity or conflict, cannot forge Route C, and cannot declare a permanent
            disappearance — those are server-side. Price is raw evidence only (money quarantine).

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        CaptureAccepted | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
