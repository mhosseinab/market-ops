import type { MessageKey } from "@market-ops/locale";
import type { MarginReadinessState, QualityState } from "./types";

// Bulk candidate disposition: maps the core's OWN surfaced verdict axes —
// observation quality (STATE_MATRIX quality→capability) and margin readiness
// (readiness→pricing consequence) — onto the executable / warning / blocked
// buckets the Bulk screen must separate. This is display of the core's verdicts,
// NOT recomputation: no money, price, contribution, or approval eligibility is
// derived here. Only Complete readiness + Verified quality yields an executable
// candidate; every other axis-value is warning (analysis-only) or blocked, and a
// blocked candidate is NEVER force-included in a confirmation.

export type Disposition = "executable" | "warning" | "blocked";

export interface DispositionResult {
  readonly disposition: Disposition;
  readonly reasonKey: MessageKey;
}

export function classifyDisposition(
  quality: QualityState | undefined,
  readiness: MarginReadinessState | undefined,
): DispositionResult {
  if (quality === undefined)
    return { disposition: "blocked", reasonKey: "bulk.reason.qualityUnknown" };
  if (quality === "conflicted")
    return { disposition: "blocked", reasonKey: "bulk.reason.conflicted" };
  if (quality === "stale")
    return { disposition: "blocked", reasonKey: "bulk.reason.staleObservation" };
  if (quality === "unavailable")
    return { disposition: "blocked", reasonKey: "bulk.reason.unavailable" };
  if (quality === "unverified")
    return { disposition: "blocked", reasonKey: "bulk.reason.unverified" };

  // quality is verified or supported below.
  if (readiness === undefined || readiness === "missing")
    return { disposition: "blocked", reasonKey: "bulk.reason.missingCost" };
  if (readiness === "stale") return { disposition: "blocked", reasonKey: "bulk.reason.staleCost" };
  if (readiness === "partial") return { disposition: "warning", reasonKey: "bulk.reason.partial" };

  // readiness complete.
  if (quality === "supported")
    return { disposition: "warning", reasonKey: "bulk.reason.jitRefresh" };
  return { disposition: "executable", reasonKey: "bulk.reason.ready" };
}
