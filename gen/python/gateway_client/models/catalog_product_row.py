from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.catalog_mapping_state import CatalogMappingState

if TYPE_CHECKING:
    from ..models.observed_offer import ObservedOffer
    from ..models.owned_offer_view import OwnedOfferView


T = TypeVar("T", bound="CatalogProductRow")


@_attrs_define
class CatalogProductRow:
    """One canonical Product-workspace row (PRD §6.1), built from Product + Variant (+ Listing/Owned Offer), joined with
    identity mapping state and observation evidence. NEVER synthesized from an observation target.

        Attributes:
            variant_id (UUID):
            product_id (UUID):
            native_variant_id (int):
            native_product_id (int):
            variant_title (str):
            product_title (str):
            supplier_code (str): Seller SKU (LTR technical identifier).
            mapping_state (CatalogMappingState): The identity MAPPING STATE of a synced variant (CAT-002). `unmapped` means
                the variant has NO Market Product Identity row at all. A row that is not `confirmed` (and watched) can never
                drive an executable recommendation.
            watched (bool): True only for an active Confirmed identity with an active observation target (OBS-001).
            owned_offer (OwnedOfferView): The variant's owned offer (PRD §6.1), CAPABILITY-GATED on owned_offer_read
                (§15.2). Price is raw evidence only (money quarantine §9.1) and is present ONLY when capability is `supported`
                AND an owned offer exists; otherwise `unavailableReason` explains why and no price/stock is fabricated.
            market_offers (list[ObservedOffer]): The variant's current competitor Observed Offers, surfaced INDIVIDUALLY
                with identity and ordered deterministically by offerIdentity ascending (money quarantine forbids numeric price
                ranking). Empty when the variant is not watched or has no current offer.
    """

    variant_id: UUID
    product_id: UUID
    native_variant_id: int
    native_product_id: int
    variant_title: str
    product_title: str
    supplier_code: str
    mapping_state: CatalogMappingState
    watched: bool
    owned_offer: OwnedOfferView
    market_offers: list[ObservedOffer]

    def to_dict(self) -> dict[str, Any]:
        variant_id = str(self.variant_id)

        product_id = str(self.product_id)

        native_variant_id = self.native_variant_id

        native_product_id = self.native_product_id

        variant_title = self.variant_title

        product_title = self.product_title

        supplier_code = self.supplier_code

        mapping_state = self.mapping_state.value

        watched = self.watched

        owned_offer = self.owned_offer.to_dict()

        market_offers = []
        for market_offers_item_data in self.market_offers:
            market_offers_item = market_offers_item_data.to_dict()
            market_offers.append(market_offers_item)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "variantId": variant_id,
                "productId": product_id,
                "nativeVariantId": native_variant_id,
                "nativeProductId": native_product_id,
                "variantTitle": variant_title,
                "productTitle": product_title,
                "supplierCode": supplier_code,
                "mappingState": mapping_state,
                "watched": watched,
                "ownedOffer": owned_offer,
                "marketOffers": market_offers,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.observed_offer import ObservedOffer
        from ..models.owned_offer_view import OwnedOfferView

        d = dict(src_dict)
        variant_id = UUID(d.pop("variantId"))

        product_id = UUID(d.pop("productId"))

        native_variant_id = d.pop("nativeVariantId")

        native_product_id = d.pop("nativeProductId")

        variant_title = d.pop("variantTitle")

        product_title = d.pop("productTitle")

        supplier_code = d.pop("supplierCode")

        mapping_state = CatalogMappingState(d.pop("mappingState"))

        watched = d.pop("watched")

        owned_offer = OwnedOfferView.from_dict(d.pop("ownedOffer"))

        market_offers = []
        _market_offers = d.pop("marketOffers")
        for market_offers_item_data in _market_offers:
            market_offers_item = ObservedOffer.from_dict(market_offers_item_data)

            market_offers.append(market_offers_item)

        catalog_product_row = cls(
            variant_id=variant_id,
            product_id=product_id,
            native_variant_id=native_variant_id,
            native_product_id=native_product_id,
            variant_title=variant_title,
            product_title=product_title,
            supplier_code=supplier_code,
            mapping_state=mapping_state,
            watched=watched,
            owned_offer=owned_offer,
            market_offers=market_offers,
        )

        return catalog_product_row
