from __future__ import annotations

import datetime
from collections.abc import Mapping
from typing import TYPE_CHECKING, Any, TypeVar

from attrs import define as _attrs_define

from ..models.listing_diagnostic_entity import ListingDiagnosticEntity
from ..models.listing_diagnostic_field import ListingDiagnosticField
from ..models.listing_diagnostic_result import ListingDiagnosticResult

if TYPE_CHECKING:
    from ..models.listing_observed_meta import ListingObservedMeta


T = TypeVar("T", bound="ListingDiagnostic")


@_attrs_define
class ListingDiagnostic:
    """One READ-ONLY listing/image diagnostic (LST-001). It NAMES the observed entity + field and the rule id/version it
    was evaluated against, carries observed-value metadata (never content), a pass/warn result, a stable evidence
    reference, and the capture time of the underlying catalog data. There is deliberately NO
    remediation/generate/publish control on this record — a diagnostic reports, it never acts.

        Attributes:
            entity (ListingDiagnosticEntity): The canonical entity a listing/image diagnostic observed (§15.1). The
                diagnostic NAMES this entity so the result is never an anonymous verdict.
            field (ListingDiagnosticField): The named field a listing/image diagnostic evaluated (LST-001). P0 covers the
                seller-facing listing quality surface: title, description, image.
            rule_id (str): Stable rule identifier (LTR technical id), e.g. listing.title.present.
            rule_version (str): Version of the rule that produced this result (LTR technical id), e.g. v1.
            result (ListingDiagnosticResult): The read-only pass/warn verdict of one diagnostic. `warn` flags a field that
                needs attention (empty or not yet observed); it NEVER triggers a write, generation, or auto-fix — remediation is
                a human, out-of-band act.
            observed (ListingObservedMeta): Observed-value METADATA only — never the raw listing text or a fabricated value.
                Carries the observed state and, for a captured text field, its character length so the UI can describe WHAT was
                observed without echoing (or inventing) content.
            evidence_ref (str): Stable reference to the canonical source the diagnostic read (a reference, not content),
                e.g. catalog/variant/{nativeVariantId}.
            captured_at (datetime.datetime): Capture time of the underlying catalog data the diagnostic evaluated.
    """

    entity: ListingDiagnosticEntity
    field: ListingDiagnosticField
    rule_id: str
    rule_version: str
    result: ListingDiagnosticResult
    observed: ListingObservedMeta
    evidence_ref: str
    captured_at: datetime.datetime

    def to_dict(self) -> dict[str, Any]:
        entity = self.entity.value

        field = self.field.value

        rule_id = self.rule_id

        rule_version = self.rule_version

        result = self.result.value

        observed = self.observed.to_dict()

        evidence_ref = self.evidence_ref

        captured_at = self.captured_at.isoformat()

        field_dict: dict[str, Any] = {}

        field_dict.update(
            {
                "entity": entity,
                "field": field,
                "ruleId": rule_id,
                "ruleVersion": rule_version,
                "result": result,
                "observed": observed,
                "evidenceRef": evidence_ref,
                "capturedAt": captured_at,
            }
        )

        return field_dict

    @classmethod
    def from_dict(cls: type[T], src_dict: Mapping[str, Any]) -> T:
        from ..models.listing_observed_meta import ListingObservedMeta

        d = dict(src_dict)
        entity = ListingDiagnosticEntity(d.pop("entity"))

        field = ListingDiagnosticField(d.pop("field"))

        rule_id = d.pop("ruleId")

        rule_version = d.pop("ruleVersion")

        result = ListingDiagnosticResult(d.pop("result"))

        observed = ListingObservedMeta.from_dict(d.pop("observed"))

        evidence_ref = d.pop("evidenceRef")

        captured_at = datetime.datetime.fromisoformat(d.pop("capturedAt"))

        listing_diagnostic = cls(
            entity=entity,
            field=field,
            rule_id=rule_id,
            rule_version=rule_version,
            result=result,
            observed=observed,
            evidence_ref=evidence_ref,
            captured_at=captured_at,
        )

        return listing_diagnostic
