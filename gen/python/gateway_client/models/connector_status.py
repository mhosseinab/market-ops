from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.connector_connection_state import ConnectorConnectionState

if TYPE_CHECKING:
    from ..models.capability_status import CapabilityStatus


T = TypeVar("T", bound="ConnectorStatus")


@_attrs_define
class ConnectorStatus:
    """Reconciled connection state plus every §15.2 capability. This is the single surface chat and screens read connector
    health from (ACC-001).

        Attributes:
            marketplace_account_id (UUID):
            connection_state (ConnectorConnectionState): Whether the DK connection is currently established.
            capabilities (list[CapabilityStatus]): All nine §15.2 capabilities, always present.
    """

    marketplace_account_id: UUID
    connection_state: ConnectorConnectionState
    capabilities: list[CapabilityStatus]

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        connection_state = self.connection_state.value

        capabilities = []
        for capabilities_item_data in self.capabilities:
            capabilities_item = capabilities_item_data.to_dict()
            capabilities.append(capabilities_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplaceAccountId": marketplace_account_id,
                "connectionState": connection_state,
                "capabilities": capabilities,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.capability_status import CapabilityStatus

        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        connection_state = ConnectorConnectionState(d.pop("connectionState"))

        capabilities = []
        _capabilities = d.pop("capabilities")
        for capabilities_item_data in _capabilities:
            capabilities_item = CapabilityStatus.from_dict(capabilities_item_data)

            capabilities.append(capabilities_item)

        connector_status = cls(
            marketplace_account_id=marketplace_account_id,
            connection_state=connection_state,
            capabilities=capabilities,
        )

        return connector_status
