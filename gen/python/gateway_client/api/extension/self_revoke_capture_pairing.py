from http import HTTPStatus
from typing import Any, cast

import httpx

from ...client import AuthenticatedClient
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs() -> dict[str, Any]:

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/ext/pairing/self-revoke",
    }

    return _kwargs


def _parse_response(*, client: AuthenticatedClient, response: httpx.Response) -> Any | ErrorEnvelope:
    if response.status_code == 204:
        response_204 = cast(Any, None)
        return response_204

    if response.status_code == 401:
        response_401 = ErrorEnvelope.from_dict(response.json())

        return response_401

    if response.status_code == 503:
        response_503 = ErrorEnvelope.from_dict(response.json())

        return response_503

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: AuthenticatedClient, response: httpx.Response) -> Response[Any | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient,
) -> Response[Any | ErrorEnvelope]:
    """Revoke the PRESENTED capture credential itself (EXT-009 kill switch).

     The credential-scoped SELF-revoke the browser extension calls so its own kill switch actually
    invalidates authorization at the authority that verifies it (PRD §14 EXT-009). The credential to
    revoke is derived SOLELY from the presented capture credential (captureAuth) — there is NO body,
    query, or path parameter, so an extension can never revoke another seller's or another device's
    pairing (tenant/credential authority is credential-derived, never caller-supplied). It revokes
    EXACTLY the presenting credential; the account-wide human kill switch remains POST
    /ext/pairing/revoke (cookieAuth). This route exists because the extension holds a Bearer capture
    credential and no human session cookie, so the account-wide route is not reachable from the
    extension. Idempotency + fail-closed posture: the FIRST call on a live credential returns 204. Any
    later call presents an already-revoked (or expired, or unknown) credential, which fails closed with
    401 BEFORE reaching the handler. A client MUST treat 401 here as CONFIRMED revocation — the
    credential is no longer valid at the authority — so a repeated revoke is idempotent in effect and a
    pending-revocation marker can always clear. Any other outcome (5xx, 503, transport failure) is NOT a
    confirmation: the client keeps capture disabled and retries. Because a client acts on 401 by
    discarding its credential, the server NEVER answers 401 for an infrastructure failure: an
    unconfigured pairing plane is 503 and a transient store failure is 500, so 401 always means the
    authority genuinely does not recognise this credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ErrorEnvelope]
    """

    kwargs = _get_kwargs()

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient,
) -> Any | ErrorEnvelope | None:
    """Revoke the PRESENTED capture credential itself (EXT-009 kill switch).

     The credential-scoped SELF-revoke the browser extension calls so its own kill switch actually
    invalidates authorization at the authority that verifies it (PRD §14 EXT-009). The credential to
    revoke is derived SOLELY from the presented capture credential (captureAuth) — there is NO body,
    query, or path parameter, so an extension can never revoke another seller's or another device's
    pairing (tenant/credential authority is credential-derived, never caller-supplied). It revokes
    EXACTLY the presenting credential; the account-wide human kill switch remains POST
    /ext/pairing/revoke (cookieAuth). This route exists because the extension holds a Bearer capture
    credential and no human session cookie, so the account-wide route is not reachable from the
    extension. Idempotency + fail-closed posture: the FIRST call on a live credential returns 204. Any
    later call presents an already-revoked (or expired, or unknown) credential, which fails closed with
    401 BEFORE reaching the handler. A client MUST treat 401 here as CONFIRMED revocation — the
    credential is no longer valid at the authority — so a repeated revoke is idempotent in effect and a
    pending-revocation marker can always clear. Any other outcome (5xx, 503, transport failure) is NOT a
    confirmation: the client keeps capture disabled and retries. Because a client acts on 401 by
    discarding its credential, the server NEVER answers 401 for an infrastructure failure: an
    unconfigured pairing plane is 503 and a transient store failure is 500, so 401 always means the
    authority genuinely does not recognise this credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient,
) -> Response[Any | ErrorEnvelope]:
    """Revoke the PRESENTED capture credential itself (EXT-009 kill switch).

     The credential-scoped SELF-revoke the browser extension calls so its own kill switch actually
    invalidates authorization at the authority that verifies it (PRD §14 EXT-009). The credential to
    revoke is derived SOLELY from the presented capture credential (captureAuth) — there is NO body,
    query, or path parameter, so an extension can never revoke another seller's or another device's
    pairing (tenant/credential authority is credential-derived, never caller-supplied). It revokes
    EXACTLY the presenting credential; the account-wide human kill switch remains POST
    /ext/pairing/revoke (cookieAuth). This route exists because the extension holds a Bearer capture
    credential and no human session cookie, so the account-wide route is not reachable from the
    extension. Idempotency + fail-closed posture: the FIRST call on a live credential returns 204. Any
    later call presents an already-revoked (or expired, or unknown) credential, which fails closed with
    401 BEFORE reaching the handler. A client MUST treat 401 here as CONFIRMED revocation — the
    credential is no longer valid at the authority — so a repeated revoke is idempotent in effect and a
    pending-revocation marker can always clear. Any other outcome (5xx, 503, transport failure) is NOT a
    confirmation: the client keeps capture disabled and retries. Because a client acts on 401 by
    discarding its credential, the server NEVER answers 401 for an infrastructure failure: an
    unconfigured pairing plane is 503 and a transient store failure is 500, so 401 always means the
    authority genuinely does not recognise this credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[Any | ErrorEnvelope]
    """

    kwargs = _get_kwargs()

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient,
) -> Any | ErrorEnvelope | None:
    """Revoke the PRESENTED capture credential itself (EXT-009 kill switch).

     The credential-scoped SELF-revoke the browser extension calls so its own kill switch actually
    invalidates authorization at the authority that verifies it (PRD §14 EXT-009). The credential to
    revoke is derived SOLELY from the presented capture credential (captureAuth) — there is NO body,
    query, or path parameter, so an extension can never revoke another seller's or another device's
    pairing (tenant/credential authority is credential-derived, never caller-supplied). It revokes
    EXACTLY the presenting credential; the account-wide human kill switch remains POST
    /ext/pairing/revoke (cookieAuth). This route exists because the extension holds a Bearer capture
    credential and no human session cookie, so the account-wide route is not reachable from the
    extension. Idempotency + fail-closed posture: the FIRST call on a live credential returns 204. Any
    later call presents an already-revoked (or expired, or unknown) credential, which fails closed with
    401 BEFORE reaching the handler. A client MUST treat 401 here as CONFIRMED revocation — the
    credential is no longer valid at the authority — so a repeated revoke is idempotent in effect and a
    pending-revocation marker can always clear. Any other outcome (5xx, 503, transport failure) is NOT a
    confirmation: the client keeps capture disabled and retries. Because a client acts on 401 by
    discarding its credential, the server NEVER answers 401 for an infrastructure failure: an
    unconfigured pairing plane is 503 and a transient store failure is 500, so 401 always means the
    authority genuinely does not recognise this credential.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Any | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
        )
    ).parsed
