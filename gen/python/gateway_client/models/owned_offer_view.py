from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.connector_capability_state import ConnectorCapabilityState
from ..models.owned_offer_unavailable_reason import OwnedOfferUnavailableReason
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.raw_amount import RawAmount


T = TypeVar("T", bound="OwnedOfferView")


@_attrs_define
class OwnedOfferView:
    """The variant's owned offer (PRD §6.1), CAPABILITY-GATED on owned_offer_read (§15.2). Price is raw evidence only
    (money quarantine §9.1) and is present ONLY when capability is `supported` AND an owned offer exists; otherwise
    `unavailableReason` explains why and no price/stock is fabricated.

        Attributes:
            capability (ConnectorCapabilityState): Capability status (PRD §15.2). Starts Unknown; becomes Supported only
                after a probe confirms behavior. Unknown never enables dependent UI.
            present (bool): Whether a canonical owned offer exists AND is renderable (capability Supported).
            unavailable_reason (None | OwnedOfferUnavailableReason | Unset): Set when present is false; null when the owned
                offer renders.
            price (None | RawAmount | Unset): Raw price evidence; null unless capability Supported and an owned offer
                exists.
            seller_stock (int | None | Unset): Owned seller stock count; null when absent or gated.
            warehouse_stock (int | None | Unset): Owned warehouse stock count; null when absent or gated.
    """

    capability: ConnectorCapabilityState
    present: bool
    unavailable_reason: None | OwnedOfferUnavailableReason | Unset = UNSET
    price: None | RawAmount | Unset = UNSET
    seller_stock: int | None | Unset = UNSET
    warehouse_stock: int | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        from ..models.raw_amount import RawAmount

        capability = self.capability.value

        present = self.present

        unavailable_reason: None | str | Unset
        if isinstance(self.unavailable_reason, Unset):
            unavailable_reason = UNSET
        elif isinstance(self.unavailable_reason, OwnedOfferUnavailableReason):
            unavailable_reason = self.unavailable_reason.value
        else:
            unavailable_reason = self.unavailable_reason

        price: dict[str, Any] | None | Unset
        if isinstance(self.price, Unset):
            price = UNSET
        elif isinstance(self.price, RawAmount):
            price = self.price.to_dict()
        else:
            price = self.price

        seller_stock: int | None | Unset
        if isinstance(self.seller_stock, Unset):
            seller_stock = UNSET
        else:
            seller_stock = self.seller_stock

        warehouse_stock: int | None | Unset
        if isinstance(self.warehouse_stock, Unset):
            warehouse_stock = UNSET
        else:
            warehouse_stock = self.warehouse_stock

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "capability": capability,
                "present": present,
            }
        )
        if unavailable_reason is not UNSET:
            field_dict["unavailableReason"] = unavailable_reason
        if price is not UNSET:
            field_dict["price"] = price
        if seller_stock is not UNSET:
            field_dict["sellerStock"] = seller_stock
        if warehouse_stock is not UNSET:
            field_dict["warehouseStock"] = warehouse_stock

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.raw_amount import RawAmount

        d = dict(src_dict)
        capability = ConnectorCapabilityState(d.pop("capability"))

        present = d.pop("present")

        def _parse_unavailable_reason(data: object) -> None | OwnedOfferUnavailableReason | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                unavailable_reason_type_1 = OwnedOfferUnavailableReason(data)

                return unavailable_reason_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | OwnedOfferUnavailableReason | Unset, data)

        unavailable_reason = _parse_unavailable_reason(d.pop("unavailableReason", UNSET))

        def _parse_price(data: object) -> None | RawAmount | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                price_type_1 = RawAmount.from_dict(data)

                return price_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | RawAmount | Unset, data)

        price = _parse_price(d.pop("price", UNSET))

        def _parse_seller_stock(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        seller_stock = _parse_seller_stock(d.pop("sellerStock", UNSET))

        def _parse_warehouse_stock(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        warehouse_stock = _parse_warehouse_stock(d.pop("warehouseStock", UNSET))

        owned_offer_view = cls(
            capability=capability,
            present=present,
            unavailable_reason=unavailable_reason,
            price=price,
            seller_stock=seller_stock,
            warehouse_stock=warehouse_stock,
        )

        return owned_offer_view
