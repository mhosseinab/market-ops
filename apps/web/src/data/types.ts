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
