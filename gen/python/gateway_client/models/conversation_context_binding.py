from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.conversation_context_kind import ConversationContextKind
from ..types import UNSET, Unset

T = TypeVar("T", bound="ConversationContextBinding")


@_attrs_define
class ConversationContextBinding:
    """The deterministic context a turn binds to the conversation (PRD §8.1 CHAT-007). Route entry and structured picker
    selection BIND the selected entity here so the gateway persists the exact kind/entity the operator sees — the chip
    can never claim a context the gateway did not receive. Binding is SERVER-VERSIONED and APPEND-ONLY: the first turn
    establishes version 1; changing the bound entity requires an EXPLICIT transition (`transition: true`) that appends a
    new version, never a silent relabel; a `contextVersion` that no longer matches the conversation's current bound
    version is REJECTED (stale) and produces no Draft. Account, context entity, and conversation provenance must belong
    to the same tenant.

        Attributes:
            kind (ConversationContextKind): The kind of entity a conversation's single deterministic context is bound to
                (PRD §8.1 CHAT-007). One canonical §15.1 record kind per value; a conversation has EXACTLY ONE active context at
                a time, never inferred from free text. `global` is the no-entity context (the operator's whole account). The
                gateway is authoritative for the bound kind — this field is the client's DECLARED binding, validated and
                versioned server-side.
            entity_id (str | Unset): The bound entity's identifier (a variant/event/recommendation/action id) — an LTR
                technical identifier, never localized. Absent for the `global` context. Stored verbatim under the caller's org-
                scoped conversation; the gateway never resolves it against another tenant (no existence oracle).
            context_version (int | Unset): The conversation's context version this turn is issued against. Absent on the
                first turn (the gateway issues version 1). On a continuation it must equal the conversation's CURRENT bound
                version; a stale/mismatched version is rejected without producing a Draft or approval card.
            transition (bool | Unset): Set true to EXPLICITLY transition the conversation's bound context to a different
                entity (appends a new version). Without it, a turn whose binding differs from the conversation's current context
                is rejected rather than silently relabeling the conversation.
    """

    kind: ConversationContextKind
    entity_id: str | Unset = UNSET
    context_version: int | Unset = UNSET
    transition: bool | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        kind = self.kind.value

        entity_id = self.entity_id

        context_version = self.context_version

        transition = self.transition

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "kind": kind,
            }
        )
        if entity_id is not UNSET:
            field_dict["entityId"] = entity_id
        if context_version is not UNSET:
            field_dict["contextVersion"] = context_version
        if transition is not UNSET:
            field_dict["transition"] = transition

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        kind = ConversationContextKind(d.pop("kind"))

        entity_id = d.pop("entityId", UNSET)

        context_version = d.pop("contextVersion", UNSET)

        transition = d.pop("transition", UNSET)

        conversation_context_binding = cls(
            kind=kind,
            entity_id=entity_id,
            context_version=context_version,
            transition=transition,
        )

        return conversation_context_binding
