"""The real authoritative READ transport over the gateway (issue #108, 108c).

:class:`GatewayReadPort` is the production :class:`~llm.flows.read_ports.ReadPort`:
a bounded, typed HTTP adapter that presents the read/Draft-only outbound gateway
credential and calls read endpoints that ALREADY exist in the frozen owned
contract (``contracts/gateway.openapi.yaml``) — ``getBriefing``,
``getMarginReadiness``, ``getRecommendationDetail``, ``listActions``,
``getGuardrails``. It adds no endpoint and changes no contract.

It deliberately mirrors :class:`~llm.flows.gateway_draft.GatewayDraftPort`:

* an INJECTED ``httpx.Client`` (tests supply ``httpx.MockTransport``; nothing here
  ever opens a real connection in a test);
* a per-request ``timeout`` strictly inside the per-tool bound, so a hung gateway
  is aborted at the transport rather than abandoned on a worker thread (#25);
* :func:`~llm.orchestrator.cancellation.raise_if_cancelled` BEFORE every call, so
  a read whose per-tool deadline already elapsed never leaves this process;
* fail-closed by contract — a non-2xx, transport error, malformed body, missing
  required field, or tenant mismatch raises
  :class:`~llm.flows.read_ports.ReadUnavailable`, never a fabricated read.

**Retry.** There is none here. A read is retried at most once at the NODE level
(§12.4 "exactly one retry", ``Settings.node_transient_retries``); retrying inside
the transport as well would stack retries, which §12.4 forbids.

**Money.** Amounts are decoded straight into
:class:`~llm.envelope.models.Money` from the gateway's signed-decimal STRING
mantissa. There is no ``float`` anywhere on this path; a mantissa that is not
``^-?[0-9]+$`` within int64 range fails the Money validator and therefore fails
the read closed (quarantine over inference), never rounds.

**Tenant.** Every call is scoped by the turn's AUTHORITATIVE
:class:`~llm.orchestrator.scope.TurnScope`. Where a response echoes its own
tenant, the echo is COMPARED to that scope and a mismatch fails closed and emits
— defence in depth over the gateway's own authorization and the database
ownership invariant (#412/#419), never a substitute for either.
"""

from __future__ import annotations

import logging
from typing import Any

import httpx
from pydantic import BaseModel, ValidationError

from llm.envelope.models import Money
from llm.flows.read_ports import (
    ActionRowRead,
    ActionsRead,
    BriefingEventRead,
    BriefingRead,
    GuardrailsRead,
    MarginReadinessRead,
    PolicyBlockerRead,
    ReadUnavailable,
    RecommendationDetailRead,
)
from llm.orchestrator.cancellation import raise_if_cancelled
from llm.orchestrator.scope import TurnScope

__all__ = ["GATEWAY_READ_METRIC", "GatewayReadPort"]

# Stable, locale-neutral telemetry identifier for the authoritative-read boundary.
# Every outcome emits (ok / http_status / transport / malformed / tenant_mismatch)
# so telemetry can tell a fail-closed degradation apart from a correct read. No
# tenant identifier, no entity id, no marketplace text and no locale copy is ever
# recorded here, and the outbound credential is NEVER logged.
GATEWAY_READ_METRIC = "llm_gateway_read_total"
_LOGGER = logging.getLogger("llm.flows.gateway_read")

# Stable machine outcome tokens (never user-facing copy).
_OK = "ok"
_HTTP_STATUS = "http_status"
_TRANSPORT = "transport"
_MALFORMED = "malformed"
_TENANT_MISMATCH = "tenant_mismatch"


class GatewayReadPort:
    """ReadPort backed by the gateway's read/Draft-only outbound credential.

    Constructed once at startup with the internal gateway base URL, the outbound
    read/Draft-only bearer, and an ``httpx.Client``. Holds no DB credential and
    no session cookie: it is a machine principal whose capability envelope
    (``perm.GatewayCan``) admits exactly the registry's read + ``draft.*``
    actions and nothing else.
    """

    def __init__(
        self,
        base_url: str,
        token: str,
        client: httpx.Client,
        *,
        timeout_seconds: float,
    ) -> None:
        self._base_url = base_url.rstrip("/")
        self._token = token
        self._client = client
        self._timeout_seconds = timeout_seconds

    # --- transport -----------------------------------------------------------

    def _get(self, path: str, params: dict[str, str]) -> dict[str, Any]:
        """Issue one bounded, cancellable GET and return the decoded JSON object.

        The request-scoped cancel token is authoritative (#25): a read whose
        per-tool deadline already elapsed aborts BEFORE the request is sent.
        """
        raise_if_cancelled()
        headers = {"Accept": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        try:
            resp = self._client.get(
                self._base_url + path,
                params=params,
                headers=headers,
                timeout=self._timeout_seconds,
            )
        except httpx.HTTPError as exc:
            # A read is idempotent and carries no ticket, so an aborted read is
            # unambiguous: nothing landed. Fail closed immediately — no retry
            # here (§12.4 allows exactly one, at the node).
            self._emit(path, _TRANSPORT)
            raise ReadUnavailable(f"read transport error on {path}: {exc}") from exc
        if resp.status_code // 100 != 2:
            self._emit(path, _HTTP_STATUS, status=resp.status_code)
            raise ReadUnavailable(f"gateway returned {resp.status_code} on {path}")
        try:
            data = resp.json()
        except ValueError as exc:
            self._emit(path, _MALFORMED)
            raise ReadUnavailable(f"malformed read response on {path}") from exc
        if not isinstance(data, dict):
            self._emit(path, _MALFORMED)
            raise ReadUnavailable(f"unexpected read response shape on {path}")
        self._emit(path, _OK)
        return data

    def _emit(self, path: str, outcome: str, *, status: int | None = None) -> None:
        record: dict[str, Any] = {
            "metric": GATEWAY_READ_METRIC,
            "path": path,
            "outcome": outcome,
        }
        if status is not None:
            record["status"] = status
        if outcome == _OK:
            _LOGGER.info("gateway_read", extra=record)
        else:
            _LOGGER.warning("gateway_read_failed", extra=record)

    def _check_tenant(self, path: str, data: dict[str, Any], scope: TurnScope) -> None:
        """Fail closed when a payload's own tenant echo is not the turn's scope.

        Defence in depth (#412/#419 closed this at the database and the gateway
        authorizes the read): if the response nonetheless describes another
        account, the turn must NOT proceed on it, and the divergence must be
        OBSERVABLE — a silently-accepted or silently-coerced foreign payload is a
        bug either way.
        """
        echoed = data.get("marketplaceAccountId")
        if isinstance(echoed, str) and echoed != scope.marketplace_account_id:
            self._emit(path, _TENANT_MISMATCH)
            raise ReadUnavailable(
                f"read response on {path} describes a different account than the "
                "turn's authoritative scope"
            )

    # --- typed reads ---------------------------------------------------------

    def briefing(self, scope: TurnScope, *, business_day: str) -> BriefingRead:
        path = "/briefing"
        data = self._get(
            path,
            {
                "marketplaceAccountId": scope.marketplace_account_id,
                "businessDay": business_day,
            },
        )
        self._check_tenant(path, data, scope)
        events = [
            BriefingEventRead(
                rank=_int(item, "rank", path),
                event_id=_str(item, "eventId", path),
                event_type=_str(item, "eventType", path),
                severity=_str(item, "severity", path),
            )
            for item in _objects(data, "events", path)
        ]
        return _build(
            BriefingRead,
            path,
            marketplace_account_id=scope.marketplace_account_id,
            business_day=_str(data, "businessDay", path),
            generated_at=_str(data, "generatedAt", path),
            events=events,
        )

    def margin_readiness(self, scope: TurnScope, *, variant_id: str) -> MarginReadinessRead:
        path = "/cost/readiness"
        data = self._get(path, {"variantId": variant_id})
        self._check_tenant(path, data, scope)
        return _build(
            MarginReadinessRead,
            path,
            variant_id=_str(data, "variantId", path),
            marketplace_account_id=scope.marketplace_account_id,
            state=_str(data, "state", path),
            missing_components=_strings(data, "missingComponents", path),
            stale_components=_strings(data, "staleComponents", path),
            computed_at=_str(data, "computedAt", path),
        )

    def recommendation_detail(
        self, scope: TurnScope, *, recommendation_id: str
    ) -> RecommendationDetailRead:
        path = "/recommendations/detail"
        data = self._get(path, {"recommendationId": recommendation_id})
        self._check_tenant(path, data, scope)
        blockers = [
            PolicyBlockerRead(
                stage=_str(item, "stage", path),
                stage_order=_int(item, "stageOrder", path),
                code=_str(item, "code", path),
            )
            for item in _objects(data, "blockers", path)
        ]
        return _build(
            RecommendationDetailRead,
            path,
            id=_str(data, "id", path),
            marketplace_account_id=_str(data, "marketplaceAccountId", path),
            variant_id=_str(data, "variantId", path),
            version=_int(data, "version", path),
            objective=_str(data, "objective", path),
            readiness=_str(data, "readiness", path),
            evidence_quality=_str(data, "evidenceQuality", path),
            approvable=_bool(data, "approvable", path),
            simulation=_bool(data, "simulation", path),
            current_price=_money(data, "currentPrice", path, required=True),
            proposed_price=_money(data, "proposedPrice", path),
            current_contribution=_money(data, "currentContribution", path),
            proposed_contribution=_money(data, "proposedContribution", path),
            evidence_observation_id=_optional_str(data, "evidenceObservationId"),
            evidence_as_of=_optional_str(data, "evidenceAsOf"),
            blockers=blockers,
        )

    def actions(self, scope: TurnScope) -> ActionsRead:
        path = "/actions"
        data = self._get(path, {"marketplaceAccountId": scope.marketplace_account_id})
        rows = [
            ActionRowRead(
                action_id=_str(item, "id", path),
                state=_str(item, "state", path),
                entity_id=_optional_str(item, "variantId"),
                price=_money(item, "price", path),
            )
            for item in _objects(data, "items", path)
        ]
        has_more = data.get("hasMore")
        return _build(
            ActionsRead,
            path,
            actions=rows,
            has_more=has_more if isinstance(has_more, bool) else False,
        )

    def guardrails(self, scope: TurnScope) -> GuardrailsRead:
        path = "/guardrails"
        data = self._get(path, {"marketplaceAccountId": scope.marketplace_account_id})
        self._check_tenant(path, data, scope)
        settings = data.get("settings")
        return _build(
            GuardrailsRead,
            path,
            marketplace_account_id=_str(data, "marketplaceAccountId", path),
            version=_int(data, "version", path),
            updated_at=_str(data, "updatedAt", path),
            settings=settings if isinstance(settings, dict) else {},
        )


# --- strict field decoding (missing/mistyped ⇒ fail closed) -------------------


def _str(data: dict[str, Any], key: str, path: str) -> str:
    value = data.get(key)
    if not isinstance(value, str) or not value:
        raise ReadUnavailable(f"gateway read response on {path} missing {key!r}")
    return value


def _optional_str(data: dict[str, Any], key: str) -> str | None:
    value = data.get(key)
    return value if isinstance(value, str) and value else None


def _int(data: dict[str, Any], key: str, path: str) -> int:
    value = data.get(key)
    # bool is an int subclass but is never a count/rank/version; a float is a
    # TYPE violation, never a value to coerce (§4.6 quarantine over inference).
    if isinstance(value, bool) or not isinstance(value, int):
        raise ReadUnavailable(f"gateway read response on {path} missing integer {key!r}")
    return value


def _bool(data: dict[str, Any], key: str, path: str) -> bool:
    value = data.get(key)
    if not isinstance(value, bool):
        raise ReadUnavailable(f"gateway read response on {path} missing boolean {key!r}")
    return value


def _strings(data: dict[str, Any], key: str, path: str) -> list[str]:
    value = data.get(key, [])
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise ReadUnavailable(f"gateway read response on {path} has a malformed {key!r}")
    return list(value)


def _objects(data: dict[str, Any], key: str, path: str) -> list[dict[str, Any]]:
    value = data.get(key, [])
    if not isinstance(value, list) or any(not isinstance(item, dict) for item in value):
        raise ReadUnavailable(f"gateway read response on {path} has a malformed {key!r}")
    return list(value)


def _money(
    data: dict[str, Any], key: str, path: str, *, required: bool = False
) -> Money | None:
    """Decode a gateway ``MoneyAmount`` into exact integer Money — never a float.

    The mantissa arrives as a signed-decimal STRING (#73 / §15.1) and is handed
    to the ``Money`` validator VERBATIM: it rejects floats, non-decimal strings,
    and out-of-int64 values, so an ambiguous amount fails the read closed instead
    of being coerced (§9.1, never-cut money correctness).
    """
    raw = data.get(key)
    if raw is None:
        if required:
            raise ReadUnavailable(f"gateway read response on {path} missing money {key!r}")
        return None
    if not isinstance(raw, dict):
        raise ReadUnavailable(f"gateway read response on {path} has a malformed {key!r}")
    try:
        return Money.model_validate(raw)
    except ValidationError as exc:
        raise ReadUnavailable(f"malformed money {key!r} on {path}: {exc}") from exc


def _build[ReadModel: BaseModel](
    model: type[ReadModel], path: str, **fields: Any
) -> ReadModel:
    """Construct a typed read result, converting a shape error into a closed read."""
    try:
        return model(**fields)
    except ValidationError as exc:
        raise ReadUnavailable(f"malformed read result on {path}: {exc}") from exc
