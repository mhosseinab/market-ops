from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.health_status import HealthStatus

if TYPE_CHECKING:
    from ..models.build_info import BuildInfo


T = TypeVar("T", bound="Health")


@_attrs_define
class Health:
    """Liveness plus build identity, returned by GET /healthz.

    Attributes:
        status (HealthStatus): Liveness marker; "ok" when the service is live.
        build (BuildInfo): Identity of the running binary.
    """

    status: HealthStatus
    build: BuildInfo

    def to_dict(self) -> dict[str, Any]:
        status = self.status.value

        build = self.build.to_dict()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "status": status,
                "build": build,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.build_info import BuildInfo

        d = dict(src_dict)
        status = HealthStatus(d.pop("status"))

        build = BuildInfo.from_dict(d.pop("build"))

        health = cls(
            status=status,
            build=build,
        )

        return health
