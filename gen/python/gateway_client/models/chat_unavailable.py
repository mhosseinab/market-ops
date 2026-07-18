from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.chat_unavailable_reason import ChatUnavailableReason

T = TypeVar("T", bound="ChatUnavailable")


@_attrs_define
class ChatUnavailable:
    """Structured disabled state for /chat. The web client renders the suggested-prompts + structured-screens fallback
    (§12.1) from this; it is NOT an error the way a 5xx failure is — screens remain fully usable.

        Attributes:
            code (str): Stable machine-readable code (screaming_snake_case).
            message (str): Human-readable summary. Localized at the edge, never in core.
            reason (ChatUnavailableReason): Why chat is unavailable. All three degrade chat ONLY — every structured screen
                stays fully functional (CHAT-009). Never inferred; set by the gateway from the kill-switch config or LLM-plane
                reachability.
    """

    code: str
    message: str
    reason: ChatUnavailableReason

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        message = self.message

        reason = self.reason.value

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "message": message,
                "reason": reason,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        reason = ChatUnavailableReason(d.pop("reason"))

        chat_unavailable = cls(
            code=code,
            message=message,
            reason=reason,
        )

        return chat_unavailable
