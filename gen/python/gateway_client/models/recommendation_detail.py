from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar, cast
from uuid import UUID

from attrs import define as _attrs_define

from ..models.margin_readiness_state import MarginReadinessState
from ..models.policy_objective import PolicyObjective
from ..models.quality_state import QualityState
from ..types import UNSET, Unset

if TYPE_CHECKING:
    from ..models.contribution_deduction import ContributionDeduction
    from ..models.money_amount import MoneyAmount
    from ..models.policy_boundary import PolicyBoundary
    from ..models.recommendation_blocker import RecommendationBlocker


T = TypeVar("T", bound="RecommendationDetail")


@_attrs_define
class RecommendationDetail:
    """The full PRC-001 record for one persisted recommendation version, including the §9.2 contribution breakdown (PD-3
    items 1/3). Every optional field is present-or-unavailable-with-reason — never a fabricated value.

        Attributes:
            id (UUID):
            marketplace_account_id (UUID):
            variant_id (UUID):
            lineage_id (UUID):
            version (int):
            objective (PolicyObjective): The optimization objective (stage 6, §9.3).
            current_price (MoneyAmount): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD §9.1).
                Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost amount is
                representable because the account's entry currency is known; it stays excluded from executable paths until
                S16+S35.
            contribution_deductions (list[ContributionDeduction]): The §9.2 contribution breakdown (PRC-001 inputs) for the
                proposed contribution — the SAME deductions persisted on the recommendation row, decoded verbatim (never
                recomputed/fabricated at read time).
            readiness (MarginReadinessState): The four closed margin-readiness states (CST-003). Only `complete` may drive
                an executable recommendation; `partial` may show analysis but no approval control; `stale` and `missing` block.
            evidence_quality (QualityState): The SIX evidence-quality states (PRD §10.3, OBS-003). The set is closed; each
                state has a fixed display/recommend/execute consequence. An expired value is `stale` and can never satisfy a
                current-data gate (OBS-004).
            assumptions (list[str]):
            blockers (list[RecommendationBlocker]):
            approvable (bool):
            simulation (bool):
            event_id (UUID | Unset): The driving market event, when this recommendation is event-driven.
            proposed_price (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent) triple (PRD
                §9.1). Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact integer. A cost
                amount is representable because the account's entry currency is known; it stays excluded from executable paths
                until S16+S35.
            current_contribution (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent)
                triple (PRD §9.1). Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact
                integer. A cost amount is representable because the account's entry currency is known; it stays excluded from
                executable paths until S16+S35.
            proposed_contribution (MoneyAmount | Unset): An exact monetary amount as the (mantissa, currency, exponent)
                triple (PRD §9.1). Value = mantissa × 10^exponent currency units. There is NO float: mantissa is an exact
                integer. A cost amount is representable because the account's entry currency is known; it stays excluded from
                executable paths until S16+S35.
            allowed_range (PolicyBoundary | Unset): The marketplace price boundary (stage 1, §9.2). `known` false is an
                UNKNOWN boundary and blocks (§16). Min/Max are required when known.
            evidence_observation_id (UUID | Unset):
            evidence_as_of (datetime.datetime | Unset):
            expires_at (datetime.datetime | Unset):
    """

    id: UUID
    marketplace_account_id: UUID
    variant_id: UUID
    lineage_id: UUID
    version: int
    objective: PolicyObjective
    current_price: MoneyAmount
    contribution_deductions: list[ContributionDeduction]
    readiness: MarginReadinessState
    evidence_quality: QualityState
    assumptions: list[str]
    blockers: list[RecommendationBlocker]
    approvable: bool
    simulation: bool
    event_id: UUID | Unset = UNSET
    proposed_price: MoneyAmount | Unset = UNSET
    current_contribution: MoneyAmount | Unset = UNSET
    proposed_contribution: MoneyAmount | Unset = UNSET
    allowed_range: PolicyBoundary | Unset = UNSET
    evidence_observation_id: UUID | Unset = UNSET
    evidence_as_of: datetime.datetime | Unset = UNSET
    expires_at: datetime.datetime | Unset = UNSET

    def to_dict(self) -> dict[str, Any]:
        id = str(self.id)

        marketplace_account_id = str(self.marketplace_account_id)

        variant_id = str(self.variant_id)

        lineage_id = str(self.lineage_id)

        version = self.version

        objective = self.objective.value

        current_price = self.current_price.to_dict()

        contribution_deductions = []
        for contribution_deductions_item_data in self.contribution_deductions:
            contribution_deductions_item = contribution_deductions_item_data.to_dict()
            contribution_deductions.append(contribution_deductions_item)

        readiness = self.readiness.value

        evidence_quality = self.evidence_quality.value

        assumptions = self.assumptions

        blockers = []
        for blockers_item_data in self.blockers:
            blockers_item = blockers_item_data.to_dict()
            blockers.append(blockers_item)

        approvable = self.approvable

        simulation = self.simulation

        event_id: str | Unset = UNSET
        if not isinstance(self.event_id, Unset):
            event_id = str(self.event_id)

        proposed_price: dict[str, Any] | Unset = UNSET
        if not isinstance(self.proposed_price, Unset):
            proposed_price = self.proposed_price.to_dict()

        current_contribution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.current_contribution, Unset):
            current_contribution = self.current_contribution.to_dict()

        proposed_contribution: dict[str, Any] | Unset = UNSET
        if not isinstance(self.proposed_contribution, Unset):
            proposed_contribution = self.proposed_contribution.to_dict()

        allowed_range: dict[str, Any] | Unset = UNSET
        if not isinstance(self.allowed_range, Unset):
            allowed_range = self.allowed_range.to_dict()

        evidence_observation_id: str | Unset = UNSET
        if not isinstance(self.evidence_observation_id, Unset):
            evidence_observation_id = str(self.evidence_observation_id)

        evidence_as_of: str | Unset = UNSET
        if not isinstance(self.evidence_as_of, Unset):
            evidence_as_of = self.evidence_as_of.isoformat()

        expires_at: str | Unset = UNSET
        if not isinstance(self.expires_at, Unset):
            expires_at = self.expires_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "id": id,
                "marketplaceAccountId": marketplace_account_id,
                "variantId": variant_id,
                "lineageId": lineage_id,
                "version": version,
                "objective": objective,
                "currentPrice": current_price,
                "contributionDeductions": contribution_deductions,
                "readiness": readiness,
                "evidenceQuality": evidence_quality,
                "assumptions": assumptions,
                "blockers": blockers,
                "approvable": approvable,
                "simulation": simulation,
            }
        )
        if event_id is not UNSET:
            field_dict["eventId"] = event_id
        if proposed_price is not UNSET:
            field_dict["proposedPrice"] = proposed_price
        if current_contribution is not UNSET:
            field_dict["currentContribution"] = current_contribution
        if proposed_contribution is not UNSET:
            field_dict["proposedContribution"] = proposed_contribution
        if allowed_range is not UNSET:
            field_dict["allowedRange"] = allowed_range
        if evidence_observation_id is not UNSET:
            field_dict["evidenceObservationId"] = evidence_observation_id
        if evidence_as_of is not UNSET:
            field_dict["evidenceAsOf"] = evidence_as_of
        if expires_at is not UNSET:
            field_dict["expiresAt"] = expires_at

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.contribution_deduction import ContributionDeduction
        from ..models.money_amount import MoneyAmount
        from ..models.policy_boundary import PolicyBoundary
        from ..models.recommendation_blocker import RecommendationBlocker

        d = dict(src_dict)
        id = UUID(d.pop("id"))

        marketplace_account_id = UUID(d.pop("marketplaceAccountId"))

        variant_id = UUID(d.pop("variantId"))

        lineage_id = UUID(d.pop("lineageId"))

        version = d.pop("version")

        objective = PolicyObjective(d.pop("objective"))

        current_price = MoneyAmount.from_dict(d.pop("currentPrice"))

        contribution_deductions = []
        _contribution_deductions = d.pop("contributionDeductions")
        for contribution_deductions_item_data in _contribution_deductions:
            contribution_deductions_item = ContributionDeduction.from_dict(contribution_deductions_item_data)

            contribution_deductions.append(contribution_deductions_item)

        readiness = MarginReadinessState(d.pop("readiness"))

        evidence_quality = QualityState(d.pop("evidenceQuality"))

        assumptions = cast(list[str], d.pop("assumptions"))

        blockers = []
        _blockers = d.pop("blockers")
        for blockers_item_data in _blockers:
            blockers_item = RecommendationBlocker.from_dict(blockers_item_data)

            blockers.append(blockers_item)

        approvable = d.pop("approvable")

        simulation = d.pop("simulation")

        _event_id = d.pop("eventId", UNSET)
        event_id: UUID | Unset
        if isinstance(_event_id, Unset):
            event_id = UNSET
        else:
            event_id = UUID(_event_id)

        _proposed_price = d.pop("proposedPrice", UNSET)
        proposed_price: MoneyAmount | Unset
        if isinstance(_proposed_price, Unset):
            proposed_price = UNSET
        else:
            proposed_price = MoneyAmount.from_dict(_proposed_price)

        _current_contribution = d.pop("currentContribution", UNSET)
        current_contribution: MoneyAmount | Unset
        if isinstance(_current_contribution, Unset):
            current_contribution = UNSET
        else:
            current_contribution = MoneyAmount.from_dict(_current_contribution)

        _proposed_contribution = d.pop("proposedContribution", UNSET)
        proposed_contribution: MoneyAmount | Unset
        if isinstance(_proposed_contribution, Unset):
            proposed_contribution = UNSET
        else:
            proposed_contribution = MoneyAmount.from_dict(_proposed_contribution)

        _allowed_range = d.pop("allowedRange", UNSET)
        allowed_range: PolicyBoundary | Unset
        if isinstance(_allowed_range, Unset):
            allowed_range = UNSET
        else:
            allowed_range = PolicyBoundary.from_dict(_allowed_range)

        _evidence_observation_id = d.pop("evidenceObservationId", UNSET)
        evidence_observation_id: UUID | Unset
        if isinstance(_evidence_observation_id, Unset):
            evidence_observation_id = UNSET
        else:
            evidence_observation_id = UUID(_evidence_observation_id)

        _evidence_as_of = d.pop("evidenceAsOf", UNSET)
        evidence_as_of: datetime.datetime | Unset
        if isinstance(_evidence_as_of, Unset):
            evidence_as_of = UNSET
        else:
            evidence_as_of = datetime.datetime.fromisoformat(_evidence_as_of)

        _expires_at = d.pop("expiresAt", UNSET)
        expires_at: datetime.datetime | Unset
        if isinstance(_expires_at, Unset):
            expires_at = UNSET
        else:
            expires_at = datetime.datetime.fromisoformat(_expires_at)

        recommendation_detail = cls(
            id=id,
            marketplace_account_id=marketplace_account_id,
            variant_id=variant_id,
            lineage_id=lineage_id,
            version=version,
            objective=objective,
            current_price=current_price,
            contribution_deductions=contribution_deductions,
            readiness=readiness,
            evidence_quality=evidence_quality,
            assumptions=assumptions,
            blockers=blockers,
            approvable=approvable,
            simulation=simulation,
            event_id=event_id,
            proposed_price=proposed_price,
            current_contribution=current_contribution,
            proposed_contribution=proposed_contribution,
            allowed_range=allowed_range,
            evidence_observation_id=evidence_observation_id,
            evidence_as_of=evidence_as_of,
            expires_at=expires_at,
        )

        return recommendation_detail
