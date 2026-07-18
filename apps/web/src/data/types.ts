import type { components } from "@market-ops/gen-ts";

// Convenience aliases over the GENERATED schema component types (read-only
// artifact). Screens render exactly these shapes; a mismatch is escalated to
// api_data_contracts, never hand-patched here.
export type ConnectorStatus = components["schemas"]["ConnectorStatus"];
export type CapabilityStatus = components["schemas"]["CapabilityStatus"];
export type ConnectorCapability = components["schemas"]["ConnectorCapability"];
export type ConnectorCapabilityState = components["schemas"]["ConnectorCapabilityState"];
export type ConnectorConnectionState = components["schemas"]["ConnectorConnectionState"];

export type ObservationTarget = components["schemas"]["ObservationTarget"];
export type ObservedOffer = components["schemas"]["ObservedOffer"];
export type Observation = components["schemas"]["Observation"];
export type AvailabilityStatus = components["schemas"]["AvailabilityStatus"];
export type QualityState = components["schemas"]["QualityState"];
export type RawAmount = components["schemas"]["RawAmount"];

export type MarginReadiness = components["schemas"]["MarginReadiness"];
export type MarginReadinessState = components["schemas"]["MarginReadinessState"];
export type CostProfileVersion = components["schemas"]["CostProfileVersion"];
export type CostComponent = components["schemas"]["CostComponent"];
export type MoneyAmount = components["schemas"]["MoneyAmount"];

export type CostImportPreview = components["schemas"]["CostImportPreview"];
export type CostImportRow = components["schemas"]["CostImportRow"];
export type CostImportDisposition = components["schemas"]["CostImportDisposition"];
export type CostImportCommitResult = components["schemas"]["CostImportCommitResult"];
export type SingleCostEntryRequest = components["schemas"]["SingleCostEntryRequest"];

export type NeedsReviewItem = components["schemas"]["NeedsReviewItem"];
export type NeedsReviewQueue = components["schemas"]["NeedsReviewQueue"];

// ── S27: Today / events / recommendation + approval ─────────────────────────
export type MarketEvent = components["schemas"]["MarketEvent"];
export type MarketEventList = components["schemas"]["MarketEventList"];
export type TodayFeed = components["schemas"]["TodayFeed"];
export type RankedEvent = components["schemas"]["RankedEvent"];
export type EventRankFactors = components["schemas"]["EventRankFactors"];
export type EventExposure = components["schemas"]["EventExposure"];
export type EventType = components["schemas"]["EventType"];
export type EventSeverity = components["schemas"]["EventSeverity"];
export type EventLifecycleState = components["schemas"]["EventLifecycleState"];
export type EventRelevanceKind = components["schemas"]["EventRelevanceKind"];

export type ApprovalCardView = components["schemas"]["ApprovalCardView"];
export type ApprovalBinding = components["schemas"]["ApprovalBinding"];
export type ApprovalState = components["schemas"]["ApprovalState"];
export type ApprovalInvalidationReason = components["schemas"]["ApprovalInvalidationReason"];
export type ApprovalConfirmRequest = components["schemas"]["ApprovalConfirmRequest"];
export type ApprovalConfirmResult = components["schemas"]["ApprovalConfirmResult"];
export type ApprovalStateHistoryEntry = components["schemas"]["ApprovalStateHistoryEntry"];
export type Contribution = components["schemas"]["Contribution"];
export type ContributionDeduction = components["schemas"]["ContributionDeduction"];
