from __future__ import annotations

from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast

from attrs import define as _attrs_define

from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.action_summary import ActionSummary


T = TypeVar("T", bound="ActionList")


@_attrs_define
class ActionList:
    """One BOUNDED page of the actions queue (§17 bounded reads). `items` carries this page; `hasMore` and `nextCursor`
    carry its truthful completeness — `hasMore` is true when more matching actions exist beyond this page, and
    `nextCursor` is the opaque token to pass back as `cursor` to fetch them (null/absent on the last page). Both are
    ADDITIVE and optional so an existing client keeps working; a client that ignores them sees only the first page and
    must not treat it as the whole queue.

        Attributes:
            items (list[ActionSummary]):
            has_more (bool | Unset): True when more matching actions exist beyond this page (a further page can be fetched
                with `nextCursor`). False on the last page.
            next_cursor (None | str | Unset): Opaque keyset continuation token for the next (older) page; null when
                `hasMore` is false. Pass it back verbatim as the `cursor` query param.
    """

    items: list[ActionSummary]
    has_more: bool | Unset = UNSET
    next_cursor: None | str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        items = []
        for items_item_data in self.items:
            items_item = items_item_data.to_dict()
            items.append(items_item)

        has_more = self.has_more

        next_cursor: None | str | Unset
        if isinstance(self.next_cursor, Unset):
            next_cursor = UNSET
        else:
            next_cursor = self.next_cursor

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "items": items,
            }
        )
        if has_more is not UNSET:
            field_dict["hasMore"] = has_more
        if next_cursor is not UNSET:
            field_dict["nextCursor"] = next_cursor

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.action_summary import ActionSummary

        d = dict(src_dict)
        items = []
        _items = d.pop("items")
        for items_item_data in _items:
            items_item = ActionSummary.from_dict(items_item_data)

            items.append(items_item)

        has_more = d.pop("hasMore", UNSET)

        def _parse_next_cursor(data: object) -> None | str | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(None | str | Unset, data)

        next_cursor = _parse_next_cursor(d.pop("nextCursor", UNSET))

        action_list = cls(
            items=items,
            has_more=has_more,
            next_cursor=next_cursor,
        )

        return action_list
