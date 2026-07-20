from __future__ import annotations

from collections.abc import Mapping
from typing import Any, TypeVar, cast

from attrs import define as _attrs_define

from ..models.listing_observed_state import ListingObservedState
from ..types import UNSET, Unset

T = TypeVar("T", bound="ListingObservedMeta")


@_attrs_define
class ListingObservedMeta:
    """Observed-value METADATA only — never the raw listing text or a fabricated value. Carries the observed state and, for
    a captured text field, its character length so the UI can describe WHAT was observed without echoing (or inventing)
    content.

        Attributes:
            state (ListingObservedState): The observed-value state a diagnostic recorded for its field. `present`: the field
                carried captured content; `empty`: the field was captured but blank; `not_observed`: the connector does not yet
                surface this field's content, so it is reported as unobserved (fail closed) rather than inferred — quarantine-
                over-inference (§9.1/§4.6).
            character_length (int | None | Unset): Character (rune) length of the observed text field; null when the field
                is not a captured text value (e.g. not_observed).
    """

    state: ListingObservedState
    character_length: int | None | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state = self.state.value

        character_length: int | None | Unset
        if isinstance(self.character_length, Unset):
            character_length = UNSET
        else:
            character_length = self.character_length

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
            }
        )
        if character_length is not UNSET:
            field_dict["characterLength"] = character_length

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = ListingObservedState(d.pop("state"))

        def _parse_character_length(data: object) -> int | None | Unset:
            if data is None:
                return data
            if isinstance(data, Unset):
                return data
            return cast(int | None | Unset, data)

        character_length = _parse_character_length(d.pop("characterLength", UNSET))

        listing_observed_meta = cls(
            state=state,
            character_length=character_length,
        )

        return listing_observed_meta
