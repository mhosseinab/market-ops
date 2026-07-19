// Freshness derivation (OBS-004). This is the SINGLE source of truth both the
// SPA (apps/web/src/components/badges.tsx FreshnessPill, via
// apps/web/src/data/freshness.ts) and the extension overlay
// (apps/extension/src/lib/overlay-data.ts freshnessBucketOf) derive from — so
// the overlay's freshness bucket is byte-identical to what a human sees on the
// Market screen for the SAME offer at the SAME instant (EXT-005 parity),
// derived once here rather than duplicated and risking silent drift.
//
// The AUTHORITATIVE path (`freshnessState`) is deadline-driven: an offer is
// Stale at or after its own `freshnessDeadline`, and the fresh/aging split is
// WINDOW-RELATIVE (fresh for the first 1/6 of the capture→deadline window).
// This preserves the legacy standard-tier bands exactly (a 6h window gives
// fresh ≤60m, aging ≤360m) while honouring priority (60m) and background (24h)
// windows without granting a priority offer the standard six-hour allowance.
//
// The FIXED-MINUTE thresholds below back ONLY the deadline-LESS path
// (`freshnessStateFromAge`) for surfaces that carry no per-offer deadline —
// market events. Offer surfaces MUST use `freshnessState`.
export const FRESHNESS_FRESH_MAX_MINUTES = 60;
export const FRESHNESS_AGING_MAX_MINUTES = 360;

export type FreshnessState = "fresh" | "aging" | "stale";

// Structural input: the locale pack takes NO dependency on @market-ops/gen-ts.
// Both the web `ObservedOffer` and the extension `ObservedOffer` satisfy this.
export interface FreshnessInput {
  readonly capturedAt: string;
  readonly freshnessDeadline: string;
}

// freshnessState — DEADLINE-DRIVEN. Fails CLOSED to "stale" on any bad input
// (missing/unparseable timestamps or a non-positive window): freshness is a
// safety-relevant state, so ambiguity resolves to the least-trusting value.
export function freshnessState(offer: FreshnessInput, nowMs: number): FreshnessState {
  const deadlineMs = Date.parse(offer.freshnessDeadline);
  const capturedMs = Date.parse(offer.capturedAt);
  if (Number.isNaN(deadlineMs) || Number.isNaN(capturedMs)) return "stale";
  // Stale AT or after the authoritative deadline (exact-deadline is stale).
  if (nowMs >= deadlineMs) return "stale";
  const windowMs = deadlineMs - capturedMs;
  if (windowMs <= 0) return "stale";
  const ageMs = nowMs - capturedMs;
  const freshCutoffMs = windowMs / 6;
  return ageMs <= freshCutoffMs ? "fresh" : "aging";
}

// freshnessTransitions — the ABSOLUTE ms timestamps at which the DERIVED state
// changes: fresh→aging at capturedMs + window/6, aging→stale at the deadline.
// Returns [] when there is no future transition to schedule (already stale:
// unparseable timestamps or a non-positive window). The useNow hook filters
// these to the future ones and reschedules a single timer to the nearest.
export function freshnessTransitions(offer: FreshnessInput): number[] {
  const deadlineMs = Date.parse(offer.freshnessDeadline);
  const capturedMs = Date.parse(offer.capturedAt);
  if (Number.isNaN(deadlineMs) || Number.isNaN(capturedMs)) return [];
  const windowMs = deadlineMs - capturedMs;
  if (windowMs <= 0) return [];
  return [capturedMs + windowMs / 6, deadlineMs];
}

// freshnessStateFromAge — DEADLINE-LESS path for surfaces with no per-offer
// deadline (MARKET EVENTS ONLY). Preserves today's fixed-minute banding.
export function freshnessStateFromAge(ageMinutes: number): FreshnessState {
  if (ageMinutes <= FRESHNESS_FRESH_MAX_MINUTES) return "fresh";
  if (ageMinutes <= FRESHNESS_AGING_MAX_MINUTES) return "aging";
  return "stale";
}
