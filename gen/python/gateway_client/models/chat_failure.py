from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ChatFailure")


@_attrs_define
class ChatFailure:
    """The §12.4 structured failure state: after one automatic retry, a concise message plus a deep link to the structured
    screen. Free text only; no authority. Emitted as the `failure` frame, or when a hard bound (turn recursion limit,
    tool-call limit, timeout, token ceiling) is exceeded.

        Attributes:
            code (str): Stable machine-readable failure code (screaming_snake_case).
            message (str): Human-readable summary. Localized at the edge.
            deep_link (str | Unset): Path to the structured screen that completes the task deterministically.
    """

    code: str
    message: str
    deep_link: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        message = self.message

        deep_link = self.deep_link

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "message": message,
            }
        )
        if deep_link is not UNSET:
            field_dict["deepLink"] = deep_link

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        deep_link = d.pop("deepLink", UNSET)

        chat_failure = cls(
            code=code,
            message=message,
            deep_link=deep_link,
        )

        return chat_failure
