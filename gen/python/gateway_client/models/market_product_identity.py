from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.market_product_identity_state import MarketProductIdentityState

T = TypeVar("T", bound="MarketProductIdentity")


@_attrs_define
class MarketProductIdentity:
    """A variant's mapping to a public DK product record, with its versioned state. Separate canonical entity from the
    owned Variant/Listing (CAT-001).

        Attributes:
            id (UUID): Stable mapping id.
            marketplace_account_id (UUID):
            variant_id (UUID): The owned variant this mapping resolves.
            native_variant_id (int): DK native variant id (LTR technical identifier).
            native_product_id (int): The public DK product record the variant is mapped to.
            state (MarketProductIdentityState): Versioned Market Product Identity state (PRD §7.2 CAT-002). Only `confirmed`
                may drive an executable path; `needs_review`, `rejected`, and `obsolete` never can (identity quarantine).
            active (bool): Whether this is the variant's live mapping.
            candidate_source (str): How the candidate was created (P0 is exact_native_id only).
            version (int): Monotonic per-mapping version, bumped on every state transition.
    """

    id: UUID
    marketplace_account_id: UUID
    variant_id: UUID
    native_variant_id: int
    native_product_id: int
    state: MarketProductIdentityState
    active: bool
    candidate_source: str
    version: int

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        native_variant_id = self.native_variant_id

        native_product_id = self.native_product_id

        state = self.state.value

        active = self.active

        candidate_source = self.candidate_source

        version = self.version

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
                "nativeVariantId": native_variant_id,
                "nativeProductId": native_product_id,
                "state": state,
                "active": active,
                "candidateSource": candidate_source,
                "version": version,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        id = UUID(d.pop("id"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        native_variant_id = d.pop("nativeVariantId")

        native_product_id = d.pop("nativeProductId")

        state = MarketProductIdentityState(d.pop("state"))

        active = d.pop("active")

        candidate_source = d.pop("candidateSource")

        version = d.pop("version")

        market_product_identity = cls(
            id=id,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
            native_variant_id=native_variant_id,
            native_product_id=native_product_id,
            state=state,
            active=active,
            candidate_source=candidate_source,
            version=version,
        )

        return market_product_identity
