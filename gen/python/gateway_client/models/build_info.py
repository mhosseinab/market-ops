from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

T = TypeVar("T", bound="BuildInfo")


@_attrs_define
class BuildInfo:
    """Identity of the running binary.

    Attributes:
        version (str): Semantic version or dev marker of the running binary.
        commit (str): VCS commit the binary was built from.
        build_time (str): Build timestamp (RFC 3339, UTC).
    """

    version: str
    commit: str
    build_time: str

    def to_dict(self) -> dict[str, Any]:
        version = self.version

        commit = self.commit

        build_time = self.build_time

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "version": version,
                "commit": commit,
                "buildTime": build_time,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        version = d.pop("version")

        commit = d.pop("commit")

        build_time = d.pop("buildTime")

        build_info = cls(
            version=version,
            commit=commit,
            build_time=build_time,
        )

        return build_info
