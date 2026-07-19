from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import Any, TypeVar

from attrs import define as _attrs_define

from ..models.catalog_sync_state import CatalogSyncState
from ..types import UNSET, Unset

T = TypeVar("T", bound="CatalogSyncStatus")


@_attrs_define
class CatalogSyncStatus:
    """The account's latest catalog-sync run, derived from durable catalog_sync_runs. Onboarding advances the "sync
    catalog" step ONLY when `state` is `completed` — never from capability availability.

        Attributes:
            state (CatalogSyncState): Durable state of the latest catalog synchronization run (ACC-004/ACC-005). This is
                EVIDENCE of completed work, distinct from `catalog_read` capability support (which only means the operation is
                allowed). `none` means no sync has ever run for the account.
            last_run_at (datetime.datetime | Unset): When the latest run started (RFC 3339, UTC). Absent while `state` is
                `none`.
            detail (str | Unset): Recovery-oriented reason for a `failed` run. Free text only; carries no authority (PRD
                §8).
    """

    state: CatalogSyncState
    last_run_at: datetime.datetime | Unset = UNSET
    detail: str | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        state = self.state.value

        last_run_at: str | Unset = UNSET
        if not isinstance(self.last_run_at, Unset):
            last_run_at = self.last_run_at.isoformat()

        detail = self.detail

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "state": state,
            }
        )
        if last_run_at is not UNSET:
            field_dict["lastRunAt"] = last_run_at
        if detail is not UNSET:
            field_dict["detail"] = detail

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        d = dict(src_dict)
        state = CatalogSyncState(d.pop("state"))

        _last_run_at = d.pop("lastRunAt", UNSET)
        last_run_at: datetime.datetime | Unset
        if isinstance(_last_run_at, Unset):
            last_run_at = UNSET
        else:
            last_run_at = datetime.datetime.fromisoformat(_last_run_at)

        detail = d.pop("detail", UNSET)

        catalog_sync_status = cls(
            state=state,
            last_run_at=last_run_at,
            detail=detail,
        )

        return catalog_sync_status
