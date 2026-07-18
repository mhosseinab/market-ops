from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.chat_statement import ChatStatement


T = TypeVar("T", bound="ChatEnvelope")


@_attrs_define
class ChatEnvelope:
    """The PROPOSED typed final response envelope (S29/S37 addendum, CHAT-030/050/060 chat-vs-screen parity) — an additive
    schema artifact, NOT YET WIRED to ChatStreamEvent.envelope (see that field's description): adopting it is a breaking
    change for the current S29 web view-model and needs `web_frontend` coordination in a follow-up step. Category-
    separated statements + evidence + freshness; JSON-safe business fields only — no LangGraph/LangChain framework type
    ever appears here (plan §4.8). NEVER carries an approval control (§8, §12.3): a structured control lives only
    outside the model plane, on the same endpoints screens use. `additionalProperties: true` so an incremental FE
    migration can read both the legacy and typed fields during the transition.

        Attributes:
            statements (list[ChatStatement] | Unset):
            generated_at (datetime.datetime | Unset):
    """

    statements: list[ChatStatement] | Unset = UNSET
    generated_at: datetime.datetime | Unset = UNSET
    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:
        statements: list[dict[str, Any]] | Unset = UNSET
        if not isinstance(self.statements, Unset):
            statements = []
            for statements_item_data in self.statements:
                statements_item = statements_item_data.to_dict()
                statements.append(statements_item)

        generated_at: str | Unset = UNSET
        if not isinstance(self.generated_at, Unset):
            generated_at = self.generated_at.isoformat()

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)
        field_dict.update({})
        if statements is not UNSET:
            field_dict["statements"] = statements
        if generated_at is not UNSET:
            field_dict["generatedAt"] = generated_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.chat_statement import ChatStatement

        d = dict(src_dict)
        _statements = d.pop("statements", UNSET)
        statements: list[ChatStatement] | Unset = UNSET
        if _statements is not UNSET:
            statements = []
            for statements_item_data in _statements:
                statements_item = ChatStatement.from_dict(statements_item_data)

                statements.append(statements_item)

        _generated_at = d.pop("generatedAt", UNSET)
        generated_at: datetime.datetime | Unset
        if isinstance(_generated_at, Unset):
            generated_at = UNSET
        else:
            generated_at = datetime.datetime.fromisoformat(_generated_at)

        chat_envelope = cls(
            statements=statements,
            generated_at=generated_at,
        )

        chat_envelope.additional_properties = d
        return chat_envelope

    @property
    def additional_keys(self) -> list[str]:
        return list(self.additional_properties.keys())

    def __getitem__(self, key: str) -> Any:
        return self.additional_properties[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self.additional_properties[key] = value

    def __delitem__(self, key: str) -> None:
        del self.additional_properties[key]

    def __contains__(self, key: str) -> bool:
        return key in self.additional_properties
