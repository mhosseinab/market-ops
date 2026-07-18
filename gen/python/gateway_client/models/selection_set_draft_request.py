from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

T = TypeVar("T", bound="SelectionSetDraftRequest")


@_attrs_define
class SelectionSetDraftRequest:
    """A bulk hand-off (CHAT-050/051): compile the conversational query into a named, versioned selection set. There is NO
    chat bulk approval.

        Attributes:
            marketplace_account_id (UUID):
            query (str): The deterministic selection query (compiled to criteria).
    """

    marketplace_account_id: UUID
    query: str

    def to_dict(self) -> dict[str, Any]:
        marketplace_account_id = str(self.marketplace_account_id)

        query = self.query

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "marketplace_account_id": marketplace_account_id,
                "query": query,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        marketplace_account_id = UUID(d.pop("marketplace_account_id"))

        query = d.pop("query")

        selection_set_draft_request = cls(
            marketplace_account_id=marketplace_account_id,
            query=query,
        )

        return selection_set_draft_request
