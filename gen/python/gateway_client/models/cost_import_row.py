from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.cost_component import CostComponent
from ..models.cost_import_disposition import CostImportDisposition
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.money_amount import MoneyAmount


T = TypeVar("T", bound="CostImportRow")


@_attrs_define
class CostImportRow:
    """One preview row: raw evidence, resolution, the parsed amount (when acceptable), and the disposition + reason
    (CST-001).

        Attributes:
            row_number (int): 1-based data-row number in the file.
            sku (str): The raw SKU token from the file (LTR-isolated identifier).
            component (CostComponent): A cost component of the §9.2 contribution model. The set is closed. COGS and
                commission are always required; fulfillment/shipping/promotion are required when applicable to the listing;
                packaging/ads/returns are optional in P0 (an account policy may still require them).
            raw_value (str): The value cell exactly as entered.
            normalized_value (str): The digit-normalized numeric token (LOC-007).
            disposition (CostImportDisposition): The outcome of a preview row (CST-001, §16). `accept` will commit; `reject`
                cannot commit and carries a reason; `duplicate` is a (SKU, component) conflict within the file that blocks
                commit until resolved.
            reason (str): Stable machine reason for a non-accept row; empty for accept.
            variant_id (None | Unset | UUID): The resolved variant; null when the SKU did not resolve.
            amount (MoneyAmount | None | Unset): The parsed amount; null when the row is not an accept.
    """

    row_number: int
    sku: str
    component: CostComponent
    raw_value: str
    normalized_value: str
    disposition: CostImportDisposition
    reason: str
    variant_id: None | Unset | UUID = UNSET
    amount: MoneyAmount | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        from ..models.money_amount import MoneyAmount

        row_number = self.row_number

        sku = self.sku

        component = self.component.value

        raw_value = self.raw_value

        normalized_value = self.normalized_value

        disposition = self.disposition.value

        reason = self.reason

        variant_id: None | str | Unset
        if isinstance(self.variant_id, Unset):
            variant_id = UNSET
        elif isinstance(self.variant_id, UUID):
            variant_id = str(self.variant_id)
        else:
            variant_id = self.variant_id

        amount: dict[str, Any] | None | Unset
        if isinstance(self.amount, Unset):
            amount = UNSET
        elif isinstance(self.amount, MoneyAmount):
            amount = self.amount.to_dict()
        else:
            amount = self.amount

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "rowNumber": row_number,
                "sku": sku,
                "component": component,
                "rawValue": raw_value,
                "normalizedValue": normalized_value,
                "disposition": disposition,
                "reason": reason,
            }
        )
        if variant_id is not UNSET:
            field_dict["variantId"] = variant_id
        if amount is not UNSET:
            field_dict["amount"] = amount

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.money_amount import MoneyAmount

        d = dict(src_dict)
        row_number = d.pop("rowNumber")

        sku = d.pop("sku")

        component = CostComponent(d.pop("component"))

        raw_value = d.pop("rawValue")

        normalized_value = d.pop("normalizedValue")

        disposition = CostImportDisposition(d.pop("disposition"))

        reason = d.pop("reason")

        def _parse_variant_id(data: object) -> None | Unset | UUID:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, str):
                    raise TypeError()
                variant_id_type_0 = UUID(data)

                return variant_id_type_0
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(None | Unset | UUID, data)

        variant_id = _parse_variant_id(d.pop("variantId", UNSET))

        def _parse_amount(data: object) -> MoneyAmount | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            try:
                if not isinstance(data, dict):
                    raise TypeError()
                amount_type_1 = MoneyAmount.from_dict(data)

                return amount_type_1
            except (TypeError, ValueError, AttributeError, KeyError):
                pass
            return cast(MoneyAmount | None | Unset, data)

        amount = _parse_amount(d.pop("amount", UNSET))

        cost_import_row = cls(
            row_number=row_number,
            sku=sku,
            component=component,
            raw_value=raw_value,
            normalized_value=normalized_value,
            disposition=disposition,
            reason=reason,
            variant_id=variant_id,
            amount=amount,
        )

        return cost_import_row
