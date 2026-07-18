from http import HTTPStatus
from typing import Any

import httpx

from ...client import AuthenticatedClient, Client
from ...models.error_envelope import ErrorEnvelope
from ...models.policy_simulation_request import PolicySimulationRequest
from ...models.policy_simulation_result import PolicySimulationResult
from ...types import Response


def _get_kwargs(
    *,
    body: PolicySimulationRequest,
) -> dict[str, Any]:
    headers: dict[str, Any] = {}

    _kwargs: dict[str, Any] = {
        "method": "post",
        "url": "/policy/simulate",
    }

    _kwargs["json"] = body.to_dict()

    headers["Content-Type"] = "application/json"

    _kwargs["headers"] = headers
    return _kwargs


def _parse_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> ErrorEnvelope | PolicySimulationResult:
    if response.status_code == 200:
        response_200 = PolicySimulationResult.from_dict(response.json())

        return response_200

    response_default = ErrorEnvelope.from_dict(response.json())

    return response_default


def _build_response(
    *, client: AuthenticatedClient | Client, response: httpx.Response
) -> Response[ErrorEnvelope | PolicySimulationResult]:
    return Response(
        status_code=HTTPStatus(response.status_code),
        content=response.content,
        headers=response.headers,
        parsed=_parse_response(client=client, response=response),
    )


def sync_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PolicySimulationRequest,
) -> Response[ErrorEnvelope | PolicySimulationResult]:
    """Simulate the contribution + six-stage policy engines (non-executable).

     Runs the deterministic contribution model (§9.2) and the fixed six-stage policy order (§9.3:
    boundary → hard floor → movement cap → cooldown → strategy → objective) over fully-specified what-if
    inputs, and returns the proposed price with its contribution OR the typed blockers in policy order.
    This is a SIMULATION: the result is always labelled non-executable and carries NO approval control
    (`approvable` is always false). Free text / simulations never approve (§8, §12.3, never-cut). A
    loose movement cap or cooldown (PRC-004) is rejected with a structured error. Authoritative numbers
    come only from these engines — never from the model plane (§12.3).

    Args:
        body (PolicySimulationRequest): A fully-specified what-if for the contribution + policy
            engines. All money must share one currency and exponent (§9.1). Contribution is evaluated
            as a function of price: at any candidate price the net seller proceeds and the commission
            rate base are that price (the P0 owned-offer model), so the policy stages see a
            contribution that varies with price. `nowRfc3339` and `lastActionAt` drive the cooldown
            stage; omit `lastActionAt` when there is no prior action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | PolicySimulationResult]
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
    body: PolicySimulationRequest,
) -> ErrorEnvelope | PolicySimulationResult | None:
    """Simulate the contribution + six-stage policy engines (non-executable).

     Runs the deterministic contribution model (§9.2) and the fixed six-stage policy order (§9.3:
    boundary → hard floor → movement cap → cooldown → strategy → objective) over fully-specified what-if
    inputs, and returns the proposed price with its contribution OR the typed blockers in policy order.
    This is a SIMULATION: the result is always labelled non-executable and carries NO approval control
    (`approvable` is always false). Free text / simulations never approve (§8, §12.3, never-cut). A
    loose movement cap or cooldown (PRC-004) is rejected with a structured error. Authoritative numbers
    come only from these engines — never from the model plane (§12.3).

    Args:
        body (PolicySimulationRequest): A fully-specified what-if for the contribution + policy
            engines. All money must share one currency and exponent (§9.1). Contribution is evaluated
            as a function of price: at any candidate price the net seller proceeds and the commission
            rate base are that price (the P0 owned-offer model), so the policy stages see a
            contribution that varies with price. `nowRfc3339` and `lastActionAt` drive the cooldown
            stage; omit `lastActionAt` when there is no prior action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | PolicySimulationResult
    """

    return sync_detailed(
        client=client,
        body=body,
    ).parsed


async def asyncio_detailed(
    *,
    client: AuthenticatedClient | Client,
    body: PolicySimulationRequest,
) -> Response[ErrorEnvelope | PolicySimulationResult]:
    """Simulate the contribution + six-stage policy engines (non-executable).

     Runs the deterministic contribution model (§9.2) and the fixed six-stage policy order (§9.3:
    boundary → hard floor → movement cap → cooldown → strategy → objective) over fully-specified what-if
    inputs, and returns the proposed price with its contribution OR the typed blockers in policy order.
    This is a SIMULATION: the result is always labelled non-executable and carries NO approval control
    (`approvable` is always false). Free text / simulations never approve (§8, §12.3, never-cut). A
    loose movement cap or cooldown (PRC-004) is rejected with a structured error. Authoritative numbers
    come only from these engines — never from the model plane (§12.3).

    Args:
        body (PolicySimulationRequest): A fully-specified what-if for the contribution + policy
            engines. All money must share one currency and exponent (§9.1). Contribution is evaluated
            as a function of price: at any candidate price the net seller proceeds and the commission
            rate base are that price (the P0 owned-offer model), so the policy stages see a
            contribution that varies with price. `nowRfc3339` and `lastActionAt` drive the cooldown
            stage; omit `lastActionAt` when there is no prior action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        Response[ErrorEnvelope | PolicySimulationResult]
    """

    kwargs = _get_kwargs(
        body=body,
    )

    response = await client.get_async_httpx_client().request(**kwargs)

    return _build_response(client=client, response=response)


async def asyncio(
    *,
    client: AuthenticatedClient | Client,
    body: PolicySimulationRequest,
) -> ErrorEnvelope | PolicySimulationResult | None:
    """Simulate the contribution + six-stage policy engines (non-executable).

     Runs the deterministic contribution model (§9.2) and the fixed six-stage policy order (§9.3:
    boundary → hard floor → movement cap → cooldown → strategy → objective) over fully-specified what-if
    inputs, and returns the proposed price with its contribution OR the typed blockers in policy order.
    This is a SIMULATION: the result is always labelled non-executable and carries NO approval control
    (`approvable` is always false). Free text / simulations never approve (§8, §12.3, never-cut). A
    loose movement cap or cooldown (PRC-004) is rejected with a structured error. Authoritative numbers
    come only from these engines — never from the model plane (§12.3).

    Args:
        body (PolicySimulationRequest): A fully-specified what-if for the contribution + policy
            engines. All money must share one currency and exponent (§9.1). Contribution is evaluated
            as a function of price: at any candidate price the net seller proceeds and the commission
            rate base are that price (the P0 owned-offer model), so the policy stages see a
            contribution that varies with price. `nowRfc3339` and `lastActionAt` drive the cooldown
            stage; omit `lastActionAt` when there is no prior action.

    Raises:
        errors.UnexpectedStatus: If the server returns an undocumented status code and Client.raise_on_unexpected_status is True.
        httpx.TimeoutException: If the request takes longer than Client.timeout.

    Returns:
        ErrorEnvelope | PolicySimulationResult
    """

    return (
        await asyncio_detailed(
            client=client,
            body=body,
        )
    ).parsed
