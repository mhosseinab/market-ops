from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="NeedsReviewItem")


@_attrs_define
class NeedsReviewItem:
    """One Needs Review queue row (journey 4 step 1): the pending candidate plus the SKU / variant title / product title /
    native-id evidence a reviewer needs to confirm, reject, or defer.

        Attributes:
            identity_id (UUID):
            variant_id (UUID):
            native_variant_id (int):
            native_product_id (int):
            supplier_code (str): Seller SKU / supplier code (LTR technical identifier).
            variant_title (str):
            product_title (str):
            candidate_source (str):
            version (int):
    """

    identity_id: UUID
    variant_id: UUID
    native_variant_id: int
    native_product_id: int
    supplier_code: str
    variant_title: str
    product_title: str
    candidate_source: str
    version: int

    def to_dict(self) -> dict[str, Any]:
        identity_id = str(self.identity_id)

        variant_id = str(self.variant_id)

        native_variant_id = self.native_variant_id

        native_product_id = self.native_product_id

        supplier_code = self.supplier_code

        variant_title = self.variant_title

        product_title = self.product_title

        candidate_source = self.candidate_source

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "identityId": identity_id,
                "variantId": variant_id,
                "nativeVariantId": native_variant_id,
                "nativeProductId": native_product_id,
                "supplierCode": supplier_code,
                "variantTitle": variant_title,
                "productTitle": product_title,
                "candidateSource": candidate_source,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        identity_id = UUID(d.pop("identityId"))

        variant_id = UUID(d.pop("variantId"))

        native_variant_id = d.pop("nativeVariantId")

        native_product_id = d.pop("nativeProductId")

        supplier_code = d.pop("supplierCode")

        variant_title = d.pop("variantTitle")

        product_title = d.pop("productTitle")

        candidate_source = d.pop("candidateSource")

        version = d.pop("version")

        needs_review_item = cls(
            identity_id=identity_id,
            variant_id=variant_id,
            native_variant_id=native_variant_id,
            native_product_id=native_product_id,
            supplier_code=supplier_code,
            variant_title=variant_title,
            product_title=product_title,
            candidate_source=candidate_source,
            version=version,
        )

        return needs_review_item
