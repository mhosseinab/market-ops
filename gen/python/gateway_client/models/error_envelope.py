from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..types import UNSET, Unset

T = TypeVar("T", bound="ErrorEnvelope")


@_attrs_define
class ErrorEnvelope:
    """Canonical error shape for every gateway endpoint. Free text lives in `message`/`detail` only and never carries
    authority (PRD §8 free-text containment); `code` is the stable machine-readable discriminator.

        Attributes:
            code (str): Stable, machine-readable error code (screaming_snake_case).
            message (str): Human-readable summary. Localized at the edge, never in core.
            detail (str | Unset): Optional additional context for diagnostics.
            request_id (str | Unset): Correlation id for tracing this request across planes.
    """

    code: str
    message: str
    detail: str | Unset = UNSET
    request_id: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        code = self.code

        message = self.message

        detail = self.detail

        request_id = self.request_id

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "code": code,
                "message": message,
            }
        )
        if detail is not UNSET:
            field_dict["detail"] = detail
        if request_id is not UNSET:
            field_dict["requestId"] = request_id

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        code = d.pop("code")

        message = d.pop("message")

        detail = d.pop("detail", UNSET)

        request_id = d.pop("requestId", UNSET)

        error_envelope = cls(
            code=code,
            message=message,
            detail=detail,
            request_id=request_id,
        )

        return error_envelope
