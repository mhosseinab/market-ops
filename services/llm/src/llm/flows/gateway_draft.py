"""The real Draft-only transport over the gateway (PRD §8.2, §12.3, §19.3).

:class:`GatewayDraftPort` is the production :class:`~llm.flows.ports.DraftPort`:
an HTTP adapter that presents the read/Draft-only ``LLM_GATEWAY_TOKEN`` and calls
the gateway's Draft-create endpoints. It replaces the S20 fail-closed transport
stub for the write surface — the ONLY writes the model plane originates, each
terminal at Draft (§8.2). It cannot approve/execute/confirm: there is no such
method, and the gateway credential's capability envelope (``perm.GatewayCan``)
would reject one anyway.

Fail-closed by contract: any non-2xx, transport error, or malformed body raises
:class:`DraftUnavailable` — never a fabricated Draft (§12.4). The turn then
degrades to the structured screen.

Endpoint contract (additive gateway growth — the Go slice implements these to
match; each authorizes against the matching ``draft.*`` perm action the registry
already declares):

* ``POST /chat/cards/recommendation-draft``
  body ``{marketplace_account_id, entity_id, recommendation_id}`` →
  ``{draft_id, action_id, context_version, recommendation_version,
  parameter_version, expires_at}``
* ``POST /chat/cards/selection-set-draft``
  body ``{marketplace_account_id, query}`` →
  ``{draft_id, action_id, context_version, parameter_version, expires_at}``
* ``POST /chat/cards/level2-proposal``
  body ``{marketplace_account_id, setting_key, before_key, after_key}`` →
  ``{draft_id, action_id, context_version, parameter_version, expires_at,
  scope_key, consequence_key}``
"""

from __future__ import annotations

from typing import Any

import httpx
from pydantic import ValidationError

from llm.flows.deep_links import approval_control, bulk_control, level2_control
from llm.flows.models import DraftKind, DraftTicket, ProposalCard


class DraftUnavailable(Exception):
    """A Draft could not be created — fail closed, never fabricate one (§12.4)."""


class GatewayDraftPort:
    """DraftPort backed by the gateway's read/Draft-only credential.

    Constructed once with the gateway base URL, the read/Draft-only bearer token,
    and an ``httpx.Client`` (injected for tests). Every call fails closed.
    """

    def __init__(self, base_url: str, token: str, client: httpx.Client) -> None:
        self._base_url = base_url.rstrip("/")
        self._token = token
        self._client = client

    def _post(self, path: str, body: dict[str, Any]) -> dict[str, Any]:
        headers = {"Accept": "application/json"}
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"
        try:
            resp = self._client.post(self._base_url + path, json=body, headers=headers)
        except httpx.HTTPError as exc:
            raise DraftUnavailable(f"draft transport error on {path}: {exc}") from exc
        if resp.status_code // 100 != 2:
            raise DraftUnavailable(f"gateway returned {resp.status_code} on {path}")
        try:
            data = resp.json()
        except ValueError as exc:
            raise DraftUnavailable(f"malformed draft response on {path}") from exc
        if not isinstance(data, dict):
            raise DraftUnavailable(f"unexpected draft response shape on {path}")
        return data

    def create_recommendation_draft(
        self, *, account_id: str, entity_id: str, recommendation_id: str
    ) -> DraftTicket:
        data = self._post(
            "/chat/cards/recommendation-draft",
            {
                "marketplace_account_id": account_id,
                "entity_id": entity_id,
                "recommendation_id": recommendation_id,
            },
        )
        return _ticket(
            data,
            DraftKind.RECOMMENDATION,
            account_id,
            entity_id=entity_id,
            control_deep_link=approval_control(_require(data, "action_id")),
        )

    def create_selection_set_draft(self, *, account_id: str, query: str) -> DraftTicket:
        data = self._post(
            "/chat/cards/selection-set-draft",
            {"marketplace_account_id": account_id, "query": query},
        )
        return _ticket(
            data,
            DraftKind.SELECTION_SET,
            account_id,
            control_deep_link=bulk_control(_require(data, "action_id")),
        )

    def create_level2_proposal(
        self, *, account_id: str, setting_key: str, before_key: str, after_key: str
    ) -> ProposalCard:
        data = self._post(
            "/chat/cards/level2-proposal",
            {
                "marketplace_account_id": account_id,
                "setting_key": setting_key,
                "before_key": before_key,
                "after_key": after_key,
            },
        )
        draft = _ticket(
            data,
            DraftKind.LEVEL2_PROPOSAL,
            account_id,
            control_deep_link=level2_control(_require(data, "action_id")),
        )
        try:
            return ProposalCard(
                setting_key=setting_key,
                before_key=before_key,
                after_key=after_key,
                scope_key=_require(data, "scope_key"),
                consequence_key=_require(data, "consequence_key"),
                draft=draft,
            )
        except ValidationError as exc:
            raise DraftUnavailable(f"malformed level2 proposal: {exc}") from exc


def _require(data: dict[str, Any], key: str) -> str:
    value = data.get(key)
    if not isinstance(value, str) or not value:
        raise DraftUnavailable(f"gateway draft response missing {key!r}")
    return value


def _ticket(
    data: dict[str, Any],
    kind: DraftKind,
    account_id: str,
    *,
    entity_id: str | None = None,
    control_deep_link: str,
) -> DraftTicket:
    try:
        return DraftTicket(
            draft_kind=kind,
            draft_id=_require(data, "draft_id"),
            action_id=_require(data, "action_id"),
            account_id=account_id,
            entity_id=entity_id,
            context_version=_require(data, "context_version"),
            recommendation_version=data.get("recommendation_version"),
            parameter_version=_require(data, "parameter_version"),
            expires_at=_require(data, "expires_at"),
            control_deep_link=control_deep_link,
        )
    except ValidationError as exc:
        raise DraftUnavailable(f"malformed draft ticket: {exc}") from exc
