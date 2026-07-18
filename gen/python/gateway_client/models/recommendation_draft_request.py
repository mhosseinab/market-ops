from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="RecommendationDraftRequest")


@_attrs_define
class RecommendationDraftRequest:
    """A PrepareAction hand-off (CHAT-041): create the individual-approval Draft for one persisted, approvable
    recommendation. All identifiers are snake_case to match the LLM plane's Draft-only transport contract.

        Attributes:
            marketplace_account_id (UUID):
            entity_id (UUID): The variant (entity) the recommendation targets.
            recommendation_id (UUID):
    """

    marketplace_account_id: UUID
    entity_id: UUID
    recommendation_id: UUID

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        entity_id = str(self.entity_id)

        recommendation_id = str(self.recommendation_id)

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplace_account_id": marketplace_account_id,
                "entity_id": entity_id,
                "recommendation_id": recommendation_id,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplace_account_id"))

        entity_id = UUID(d.pop("entity_id"))

        recommendation_id = UUID(d.pop("recommendation_id"))

        recommendation_draft_request = cls(
            marketplace_account_id=marketplace_account_id,
            entity_id=entity_id,
            recommendation_id=recommendation_id,
        )

        return recommendation_draft_request
