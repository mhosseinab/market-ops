from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.bulk_approval_confirm_request import BulkApprovalConfirmRequest
from ...models.bulk_approval_confirm_result import BulkApprovalConfirmResult
from ...models.error_envelope import ErrorEnvelope
from ...types import Response


def _get_kwargs(
    *,
    body: BulkApprovalConfirmRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/approvals/bulk/confirm",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> BulkApprovalConfirmResult | ErrorEnvelope:
    if response.status_code == 200:
        response_200 = BulkApprovalConfirmResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[BulkApprovalConfirmResult | ErrorEnvelope]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: BulkApprovalConfirmRequest,
) -> Response[BulkApprovalConfirmResult | ErrorEnvelope]:
    """Confirm a bulk approval bound to one selection-set version (CHAT-052).

     Confirms a bulk approval against a SINGLE, exact selection-set version (PRD §7.5, CHAT-051/052). The
    request binds the selection-set lineage and the exact version it previewed; the server rejects the
    confirmation when that version is no longer current (any set or evidence change mints a new
    version). A valid bulk confirmation reports `executionPending` true — per-item execution lands in
    S18. This never approves from free text and never re-queries the set (no drift).

    Args:
        body (BulkApprovalConfirmRequest): A bulk approval confirmation bound to ONE exact
            selection-set version (CHAT-052). The server rejects it when the bound version is no
            longer current (any set/evidence change mints a new version).
            BULK-PROTOCOL DESIGN RECORD (d) — VERSION RANGE / ORDERING. `boundVersion` is meaningful
            ONLY together with `selectionSetLineage`: versions are monotonic WITHIN one lineage and
            version numbers from different lineages are NOT comparable. Never order, range, or diff
            versions across lineages, and never accept a bare version without its lineage — the
            binding is the PAIR.
            BULK-PROTOCOL DESIGN RECORD (a) — RESERVATION LIFECYCLE. What #90 establishes: binding a
            (lineage, version) pair under the per-lineage lock is the SELECTION reservation — it fixes
            exactly which members, dispositions and aggregate the operator authorized, and it is
            immutable for that version (#91). Each member is then authorized through its own §8.4
            individual confirm and its own durable execution intent (unique by card id). What #90 does
            NOT establish, and #87 must add: a durable `(account, variant)` EXECUTION reservation
            spanning the window between authorization and terminal external result, so two different
            selection sets (or a bulk and an individual confirmation) cannot hold concurrent in-flight
            writes for the same variant. Until #87 lands, concurrency on one variant is bounded only
            by the card-level FROM-guard and the card-id-unique intent — sufficient to prevent a
            duplicate write for one card, NOT to prevent two cards on one variant. #87 owns that
            reservation's acquire/release/expiry semantics; #90 pins only that it is keyed on
            `(account, variant)` and must be acquired BEFORE dispatch and released on a terminal
            external result.
            BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY. #87's
            `offerIdentity` on bulk confirm/item results is ADDITIVE and OPTIONAL: it must not enter
            the `required` set of any existing schema, must not change `additionalProperties: false`,
            and must never be a CLIENT ASSERTION. The server seals the disposition and the offer
            identity from its own persisted observation/recommendation state at preview time; a
            client-supplied `offerIdentity` is a selector to be validated against the sealed value,
            never an input that can widen or redirect what gets authorized. A mismatch fails closed as
            a uniform not-found, exactly like an unknown member.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BulkApprovalConfirmResult | ErrorEnvelope]
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
    body: BulkApprovalConfirmRequest,
) -> BulkApprovalConfirmResult | ErrorEnvelope | None:
    """Confirm a bulk approval bound to one selection-set version (CHAT-052).

     Confirms a bulk approval against a SINGLE, exact selection-set version (PRD §7.5, CHAT-051/052). The
    request binds the selection-set lineage and the exact version it previewed; the server rejects the
    confirmation when that version is no longer current (any set or evidence change mints a new
    version). A valid bulk confirmation reports `executionPending` true — per-item execution lands in
    S18. This never approves from free text and never re-queries the set (no drift).

    Args:
        body (BulkApprovalConfirmRequest): A bulk approval confirmation bound to ONE exact
            selection-set version (CHAT-052). The server rejects it when the bound version is no
            longer current (any set/evidence change mints a new version).
            BULK-PROTOCOL DESIGN RECORD (d) — VERSION RANGE / ORDERING. `boundVersion` is meaningful
            ONLY together with `selectionSetLineage`: versions are monotonic WITHIN one lineage and
            version numbers from different lineages are NOT comparable. Never order, range, or diff
            versions across lineages, and never accept a bare version without its lineage — the
            binding is the PAIR.
            BULK-PROTOCOL DESIGN RECORD (a) — RESERVATION LIFECYCLE. What #90 establishes: binding a
            (lineage, version) pair under the per-lineage lock is the SELECTION reservation — it fixes
            exactly which members, dispositions and aggregate the operator authorized, and it is
            immutable for that version (#91). Each member is then authorized through its own §8.4
            individual confirm and its own durable execution intent (unique by card id). What #90 does
            NOT establish, and #87 must add: a durable `(account, variant)` EXECUTION reservation
            spanning the window between authorization and terminal external result, so two different
            selection sets (or a bulk and an individual confirmation) cannot hold concurrent in-flight
            writes for the same variant. Until #87 lands, concurrency on one variant is bounded only
            by the card-level FROM-guard and the card-id-unique intent — sufficient to prevent a
            duplicate write for one card, NOT to prevent two cards on one variant. #87 owns that
            reservation's acquire/release/expiry semantics; #90 pins only that it is keyed on
            `(account, variant)` and must be acquired BEFORE dispatch and released on a terminal
            external result.
            BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY. #87's
            `offerIdentity` on bulk confirm/item results is ADDITIVE and OPTIONAL: it must not enter
            the `required` set of any existing schema, must not change `additionalProperties: false`,
            and must never be a CLIENT ASSERTION. The server seals the disposition and the offer
            identity from its own persisted observation/recommendation state at preview time; a
            client-supplied `offerIdentity` is a selector to be validated against the sealed value,
            never an input that can widen or redirect what gets authorized. A mismatch fails closed as
            a uniform not-found, exactly like an unknown member.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BulkApprovalConfirmResult | ErrorEnvelope
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: BulkApprovalConfirmRequest,
) -> Response[BulkApprovalConfirmResult | ErrorEnvelope]:
    """Confirm a bulk approval bound to one selection-set version (CHAT-052).

     Confirms a bulk approval against a SINGLE, exact selection-set version (PRD §7.5, CHAT-051/052). The
    request binds the selection-set lineage and the exact version it previewed; the server rejects the
    confirmation when that version is no longer current (any set or evidence change mints a new
    version). A valid bulk confirmation reports `executionPending` true — per-item execution lands in
    S18. This never approves from free text and never re-queries the set (no drift).

    Args:
        body (BulkApprovalConfirmRequest): A bulk approval confirmation bound to ONE exact
            selection-set version (CHAT-052). The server rejects it when the bound version is no
            longer current (any set/evidence change mints a new version).
            BULK-PROTOCOL DESIGN RECORD (d) — VERSION RANGE / ORDERING. `boundVersion` is meaningful
            ONLY together with `selectionSetLineage`: versions are monotonic WITHIN one lineage and
            version numbers from different lineages are NOT comparable. Never order, range, or diff
            versions across lineages, and never accept a bare version without its lineage — the
            binding is the PAIR.
            BULK-PROTOCOL DESIGN RECORD (a) — RESERVATION LIFECYCLE. What #90 establishes: binding a
            (lineage, version) pair under the per-lineage lock is the SELECTION reservation — it fixes
            exactly which members, dispositions and aggregate the operator authorized, and it is
            immutable for that version (#91). Each member is then authorized through its own §8.4
            individual confirm and its own durable execution intent (unique by card id). What #90 does
            NOT establish, and #87 must add: a durable `(account, variant)` EXECUTION reservation
            spanning the window between authorization and terminal external result, so two different
            selection sets (or a bulk and an individual confirmation) cannot hold concurrent in-flight
            writes for the same variant. Until #87 lands, concurrency on one variant is bounded only
            by the card-level FROM-guard and the card-id-unique intent — sufficient to prevent a
            duplicate write for one card, NOT to prevent two cards on one variant. #87 owns that
            reservation's acquire/release/expiry semantics; #90 pins only that it is keyed on
            `(account, variant)` and must be acquired BEFORE dispatch and released on a terminal
            external result.
            BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY. #87's
            `offerIdentity` on bulk confirm/item results is ADDITIVE and OPTIONAL: it must not enter
            the `required` set of any existing schema, must not change `additionalProperties: false`,
            and must never be a CLIENT ASSERTION. The server seals the disposition and the offer
            identity from its own persisted observation/recommendation state at preview time; a
            client-supplied `offerIdentity` is a selector to be validated against the sealed value,
            never an input that can widen or redirect what gets authorized. A mismatch fails closed as
            a uniform not-found, exactly like an unknown member.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[BulkApprovalConfirmResult | ErrorEnvelope]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: BulkApprovalConfirmRequest,
) -> BulkApprovalConfirmResult | ErrorEnvelope | None:
    """Confirm a bulk approval bound to one selection-set version (CHAT-052).

     Confirms a bulk approval against a SINGLE, exact selection-set version (PRD §7.5, CHAT-051/052). The
    request binds the selection-set lineage and the exact version it previewed; the server rejects the
    confirmation when that version is no longer current (any set or evidence change mints a new
    version). A valid bulk confirmation reports `executionPending` true — per-item execution lands in
    S18. This never approves from free text and never re-queries the set (no drift).

    Args:
        body (BulkApprovalConfirmRequest): A bulk approval confirmation bound to ONE exact
            selection-set version (CHAT-052). The server rejects it when the bound version is no
            longer current (any set/evidence change mints a new version).
            BULK-PROTOCOL DESIGN RECORD (d) — VERSION RANGE / ORDERING. `boundVersion` is meaningful
            ONLY together with `selectionSetLineage`: versions are monotonic WITHIN one lineage and
            version numbers from different lineages are NOT comparable. Never order, range, or diff
            versions across lineages, and never accept a bare version without its lineage — the
            binding is the PAIR.
            BULK-PROTOCOL DESIGN RECORD (a) — RESERVATION LIFECYCLE. What #90 establishes: binding a
            (lineage, version) pair under the per-lineage lock is the SELECTION reservation — it fixes
            exactly which members, dispositions and aggregate the operator authorized, and it is
            immutable for that version (#91). Each member is then authorized through its own §8.4
            individual confirm and its own durable execution intent (unique by card id). What #90 does
            NOT establish, and #87 must add: a durable `(account, variant)` EXECUTION reservation
            spanning the window between authorization and terminal external result, so two different
            selection sets (or a bulk and an individual confirmation) cannot hold concurrent in-flight
            writes for the same variant. Until #87 lands, concurrency on one variant is bounded only
            by the card-level FROM-guard and the card-id-unique intent — sufficient to prevent a
            duplicate write for one card, NOT to prevent two cards on one variant. #87 owns that
            reservation's acquire/release/expiry semantics; #90 pins only that it is keyed on
            `(account, variant)` and must be acquired BEFORE dispatch and released on a terminal
            external result.
            BULK-PROTOCOL DESIGN RECORD (e) — `offerIdentity` WIRE COMPATIBILITY. #87's
            `offerIdentity` on bulk confirm/item results is ADDITIVE and OPTIONAL: it must not enter
            the `required` set of any existing schema, must not change `additionalProperties: false`,
            and must never be a CLIENT ASSERTION. The server seals the disposition and the offer
            identity from its own persisted observation/recommendation state at preview time; a
            client-supplied `offerIdentity` is a selector to be validated against the sealed value,
            never an input that can widen or redirect what gets authorized. A mismatch fails closed as
            a uniform not-found, exactly like an unknown member.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        BulkApprovalConfirmResult | ErrorEnvelope
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
