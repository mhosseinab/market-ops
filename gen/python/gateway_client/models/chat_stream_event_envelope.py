from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define
from attrs import field as _attrs_field

T = TypeVar("T", bound="ChatStreamEventEnvelope")


@_attrs_define
class ChatStreamEventEnvelope:
    """The final typed response envelope on a `final` frame. Its internal shape (category-separated content, evidence,
    freshness) is owned and validated inside the LLM plane (§12.2). UNCHANGED in S37: narrowing this field to the new
    ChatEnvelope schema (below) is a breaking change for the S29 web consumer's current view-model (`{sections,
    evidence}`) and needs FE coordination outside this step's delegation boundary — see the S37 PD-3 addendum note on
    ChatEnvelope. The gateway relays this verbatim.

    """

    additional_properties: dict[str, Any] = _attrs_field(init=False, factory=dict)

    def to_dict(self) -> dict[str, Any]:

        field_dict: dict[str, Any] = {}
        field_dict.update(self.additional_properties)

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        chat_stream_event_envelope = cls()

        chat_stream_event_envelope.additional_properties = d
        return chat_stream_event_envelope

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
