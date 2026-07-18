from http import HTTPStatus
from typing import Any
from uuid import UUID

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.guardrail_config_view import GuardrailConfigView
from ...types import UNSET, Response


def _get_kwargs(
    *,
    marketplace_account_id: UUID,
) -> dict[str, Any]:

    params: dict[str, Any] = {}

    json_marketplace_account_id = str(marketplace_account_id)
    params["marketplaceAccountId"] = json_marketplace_account_id

    params = {k: v for k, v in params.items() if v is not UNSET and v is not None}

    _kwargs: dict[str, Any] = {
        "method": "get",
        "url": "/guardrails",
        "params": params,
    }

    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | GuardrailConfigView:
    if response.status_code == 200:
        response_200 = GuardrailConfigView.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | GuardrailConfigView]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | GuardrailConfigView]:
    """Read an account's L3 commercial guardrails (PD-3 item 6).

     Returns the account's persisted L3 commercial guardrails (contribution floor, movement cap,
    cooldown, strategy enablement). Reading is L1, every role. Absent (never configured) is a structured
    404, never a fabricated default.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | GuardrailConfigView]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = client.get_httpx_client().request(
        **kwargs,
    )

    return _build_response(client=client, response=response)


def sync(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | GuardrailConfigView | None:
    """Read an account's L3 commercial guardrails (PD-3 item 6).

     Returns the account's persisted L3 commercial guardrails (contribution floor, movement cap,
    cooldown, strategy enablement). Reading is L1, every role. Absent (never configured) is a structured
    404, never a fabricated default.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | GuardrailConfigView
    """

    return sync_detailed(
        client=client,
        marketplace_account_id=marketplace_account_id,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> Response[ErrorEnvelope | GuardrailConfigView]:
    """Read an account's L3 commercial guardrails (PD-3 item 6).

     Returns the account's persisted L3 commercial guardrails (contribution floor, movement cap,
    cooldown, strategy enablement). Reading is L1, every role. Absent (never configured) is a structured
    404, never a fabricated default.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | GuardrailConfigView]
    """

    kwargs = _get_kwargs(
        marketplace_account_id=marketplace_account_id,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    marketplace_account_id: UUID,
) -> ErrorEnvelope | GuardrailConfigView | None:
    """Read an account's L3 commercial guardrails (PD-3 item 6).

     Returns the account's persisted L3 commercial guardrails (contribution floor, movement cap,
    cooldown, strategy enablement). Reading is L1, every role. Absent (never configured) is a structured
    404, never a fabricated default.

    Args:
        marketplace_account_id (UUID):

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | GuardrailConfigView
    """

    return (
        await asyncio_detailed(
            client=client,
            marketplace_account_id=marketplace_account_id,
        )
    ).parsed
