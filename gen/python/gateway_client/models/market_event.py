from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar
from uuid import UUID

from attrs import define as _attrs_define

from ..models.event_lifecycle_state import EventLifecycleState
from ..models.event_severity import EventSeverity
from ..models.event_type import EventType
from ..models.quality_state import QualityState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.event_rank_factors import EventRankFactors


T = TypeVar("T", bound="MarketEvent")


@_attrs_define
class MarketEvent:
    """A market event lifecycle record (PRD §7.4, §15.1). It cites its observation evidence with the observed quality state
    as-is (never upgraded) and carries its versioned materiality-threshold provenance (EVT-002). Exposure obeys EVT-005.

        Attributes:
            id (UUID):
            marketplace_account_id (UUID):
            variant_id (UUID):
            type_ (EventType): One of the five P0 market-event types (PRD §7.4 EVT-001). The set is closed.
            severity (EventSeverity): The closed, ordered severity set for a market event.
            state (EventLifecycleState): The §15.1 market-event lifecycle state. A duplicate signal updates the open record
                (EVT-003); resolved/expired free the dedup key.
            factors (EventRankFactors): The three EVT-004 ranking factors for one event, exposed so the UI can show why an
                event ranks where it does. Confidence and urgency are basis points (0..10000); exposure is the EventExposure
                (unknown stays unknown).
            evidence_quality (QualityState): The SIX evidence-quality states (PRD §10.3, OBS-003). The set is closed; each
                state has a fixed display/recommend/execute consequence. An expired value is `stale` and can never satisfy a
                current-data gate (OBS-004).
            first_detected_at (datetime.datetime):
            last_evidence_at (datetime.datetime):
            expires_at (datetime.datetime):
            evidence_update_count (int): How many times a duplicate signal updated this open record (EVT-003).
            target_id (UUID | Unset): The observation target, when the event has one.
            threshold_version (int | Unset): The materiality threshold version that fired the event (EVT-002).
            evidence_observation_id (UUID | Unset): The cited observation, when the event has one.
            evidence_ref (str | Unset): Opaque reference to the cited evidence.
            resolved_at (datetime.datetime | Unset):
    """

    id: UUID
    marketplace_account_id: UUID
    variant_id: UUID
    type_: EventType
    severity: EventSeverity
    state: EventLifecycleState
    factors: EventRankFactors
    evidence_quality: QualityState
    first_detected_at: datetime.datetime
    last_evidence_at: datetime.datetime
    expires_at: datetime.datetime
    evidence_update_count: int
    target_id: UUID | Unset = UNSET
    threshold_version: int | Unset = UNSET
    evidence_observation_id: UUID | Unset = UNSET
    evidence_ref: str | Unset = UNSET
    resolved_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        type_ = self.type_.value

        severity = self.severity.value

        state = self.state.value

        factors = self.factors.to_dict()

        evidence_quality = self.evidence_quality.value

        first_detected_at = self.first_detected_at.isoformat()

        last_evidence_at = self.last_evidence_at.isoformat()

        expires_at = self.expires_at.isoformat()

        evidence_update_count = self.evidence_update_count

        target_id: str | Unset = UNSET
        if not isinstance(self.target_id, Unset):
            target_id = str(self.target_id)

        threshold_version = self.threshold_version

        evidence_observation_id: str | Unset = UNSET
        if not isinstance(self.evidence_observation_id, Unset):
            evidence_observation_id = str(self.evidence_observation_id)

        evidence_ref = self.evidence_ref

        resolved_at: str | Unset = UNSET
        if not isinstance(self.resolved_at, Unset):
            resolved_at = self.resolved_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
                "type": type_,
                "severity": severity,
                "state": state,
                "factors": factors,
                "evidenceQuality": evidence_quality,
                "firstDetectedAt": first_detected_at,
                "lastEvidenceAt": last_evidence_at,
                "expiresAt": expires_at,
                "evidenceUpdateCount": evidence_update_count,
            }
        )
        if target_id is not UNSET:
            field_dict["targetId"] = target_id
        if threshold_version is not UNSET:
            field_dict["thresholdVersion"] = threshold_version
        if evidence_observation_id is not UNSET:
            field_dict["evidenceObservationId"] = evidence_observation_id
        if evidence_ref is not UNSET:
            field_dict["evidenceRef"] = evidence_ref
        if resolved_at is not UNSET:
            field_dict["resolvedAt"] = resolved_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.event_rank_factors import EventRankFactors

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        type_ = EventType(d.pop("type"))

        severity = EventSeverity(d.pop("severity"))

        state = EventLifecycleState(d.pop("state"))

        factors = EventRankFactors.from_dict(d.pop("factors"))

        evidence_quality = QualityState(d.pop("evidenceQuality"))

        first_detected_at = datetime.datetime.fromisoformat(d.pop("firstDetectedAt"))

        last_evidence_at = datetime.datetime.fromisoformat(d.pop("lastEvidenceAt"))

        expires_at = datetime.datetime.fromisoformat(d.pop("expiresAt"))

        evidence_update_count = d.pop("evidenceUpdateCount")

        _target_id = d.pop("targetId", UNSET)
        target_id: UUID | Unset
        if isinstance(_target_id, Unset):
            target_id = UNSET
        else:
            target_id = UUID(_target_id)

        threshold_version = d.pop("thresholdVersion", UNSET)

        _evidence_observation_id = d.pop("evidenceObservationId", UNSET)
        evidence_observation_id: UUID | Unset
        if isinstance(_evidence_observation_id, Unset):
            evidence_observation_id = UNSET
        else:
            evidence_observation_id = UUID(_evidence_observation_id)

        evidence_ref = d.pop("evidenceRef", UNSET)

        _resolved_at = d.pop("resolvedAt", UNSET)
        resolved_at: datetime.datetime | Unset
        if isinstance(_resolved_at, Unset):
            resolved_at = UNSET
        else:
            resolved_at = datetime.datetime.fromisoformat(_resolved_at)

        market_event = cls(
            id=id,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
            type_=type_,
            severity=severity,
            state=state,
            factors=factors,
            evidence_quality=evidence_quality,
            first_detected_at=first_detected_at,
            last_evidence_at=last_evidence_at,
            expires_at=expires_at,
            evidence_update_count=evidence_update_count,
            target_id=target_id,
            threshold_version=threshold_version,
            evidence_observation_id=evidence_observation_id,
            evidence_ref=evidence_ref,
            resolved_at=resolved_at,
        )

        return market_event
