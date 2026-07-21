from http import HTTPStatus
from typing import Any

import httpx

from ...client import Client
from ...models.error_envelope import ErrorEnvelope
from ...models.guardrail_config_view import GuardrailConfigView
from ...models.guardrail_write_request import GuardrailWriteRequest
from ...types import Response


def _get_kwargs(
    *,
    body: GuardrailWriteRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/guardrails",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(*, client: Client, response: httpx.Response) -> ErrorEnvelope | GuardrailConfigView:
    if response.status_code == 200:
        response_200 = GuardrailConfigView.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(*, client: Client, response: httpx.Response) -> Response[ErrorEnvelope | GuardrailConfigView]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: Client,
    body: GuardrailWriteRequest,
) -> Response[ErrorEnvelope | GuardrailConfigView]:
    r"""Write an account's L3 commercial guardrails, Owner only (PD-3 item 6).

     Sets the account's L3 commercial guardrails. Owner ONLY (guardrail.write) — Operator and Internal
    are denied, and the read/Draft-only LLM machine credential can never reach this endpoint (§12.3,
    \"guardrail-write is never an LLM-plane tool\"). Every write appends an append-only AUD-001 audit
    record ATOMICALLY with the mutation, on the SAME transaction: the write never commits without its
    audit row.

    Args:
        body (GuardrailWriteRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | GuardrailConfigView]
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
    body: GuardrailWriteRequest,
) -> ErrorEnvelope | GuardrailConfigView | None:
    r"""Write an account's L3 commercial guardrails, Owner only (PD-3 item 6).

     Sets the account's L3 commercial guardrails. Owner ONLY (guardrail.write) — Operator and Internal
    are denied, and the read/Draft-only LLM machine credential can never reach this endpoint (§12.3,
    \"guardrail-write is never an LLM-plane tool\"). Every write appends an append-only AUD-001 audit
    record ATOMICALLY with the mutation, on the SAME transaction: the write never commits without its
    audit row.

    Args:
        body (GuardrailWriteRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | GuardrailConfigView
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: Client,
    body: GuardrailWriteRequest,
) -> Response[ErrorEnvelope | GuardrailConfigView]:
    r"""Write an account's L3 commercial guardrails, Owner only (PD-3 item 6).

     Sets the account's L3 commercial guardrails. Owner ONLY (guardrail.write) — Operator and Internal
    are denied, and the read/Draft-only LLM machine credential can never reach this endpoint (§12.3,
    \"guardrail-write is never an LLM-plane tool\"). Every write appends an append-only AUD-001 audit
    record ATOMICALLY with the mutation, on the SAME transaction: the write never commits without its
    audit row.

    Args:
        body (GuardrailWriteRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | GuardrailConfigView]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: Client,
    body: GuardrailWriteRequest,
) -> ErrorEnvelope | GuardrailConfigView | None:
    r"""Write an account's L3 commercial guardrails, Owner only (PD-3 item 6).

     Sets the account's L3 commercial guardrails. Owner ONLY (guardrail.write) — Operator and Internal
    are denied, and the read/Draft-only LLM machine credential can never reach this endpoint (§12.3,
    \"guardrail-write is never an LLM-plane tool\"). Every write appends an append-only AUD-001 audit
    record ATOMICALLY with the mutation, on the SAME transaction: the write never commits without its
    audit row.

    Args:
        body (GuardrailWriteRequest):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | GuardrailConfigView
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
