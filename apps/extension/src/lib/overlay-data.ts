import type { components } from "@market-ops/gen-ts";
import { type FreshnessState, freshnessState } from "@market-ops/locale";

export type ObservedOffer = components["schemas"]["ObservedOffer"];
export type ObservationTarget = components["schemas"]["ObservationTarget"];

// Overlay view model (EXT-005): offers, seller count, lowest qualifying offer,
// freshness, and quality. Every field here is RENDERED from data the gateway
// already returns for the account's own Observed Offers — the SAME rows the
// Market screen (apps/web/src/screens/Market.tsx) reads from
// `/observation/observed-offers` + `/observation/targets`. This module does the
// SAME kind of presentational aggregation Market.tsx does client-side (row
// counts, freshness buckets) — it NEVER derives a Money, a margin, or any
// commercial value; price stays raw evidence (RawAmount), never promoted.
//
// `freshnessBucketOf` DELEGATES to the shared, deadline-driven `freshnessState`
// (packages/locale/src/freshness.ts) — the SAME derivation Market.tsx's
// FreshnessPill uses — passing the offer's OWN authoritative `freshnessDeadline`
// (a REQUIRED field on ObservedOffer, OBS-004). So the overlay's freshness
// bucket is byte-identical to what a human sees on the Market screen for the
// same offer at the same instant (overlay-parity test) — a SINGLE source of
// truth, never a duplicated threshold that could silently drift, and a
// priority-tier offer flips to Stale at ITS deadline, not a fixed six hours.
export type FreshnessBucket = FreshnessState;

export type RawAmount = components["schemas"]["RawAmount"];

// Lowest-qualifying comparison result (issue #157). Raw marketplace prices stay
// QUARANTINED evidence (RawAmount): their source unit is validation-gated (Gate
// 0a) and NOT yet canonicalized to Money — no currency, no exponent, no
// conversion (PRD §9.1 / §4.6 money-unit quarantine). Ordering raw amounts is
// valid ONLY within a single, exactly-matching source-unit token. Across
// incompatible units the overlay reports the comparison UNAVAILABLE (the
// conflicting unit tokens are retained on `conflicted` as evidence for a future
// diagnostic, not rendered) — it NEVER performs client-authored conversion and
// NEVER emits one cross-unit minimum.
export type LowestQualifying =
  // No qualifying (in-stock/limited) offer exists — honest absence.
  | { readonly kind: "none" }
  // Exactly one compatible source unit among qualifying offers: the lowest raw
  // amount within that unit, preserved verbatim as evidence.
  | { readonly kind: "single"; readonly amount: RawAmount }
  // Two or more incompatible source-unit tokens (or an unknown/blank token that
  // stays quarantined, never inferred) — comparison unavailable.
  | { readonly kind: "conflicted"; readonly units: readonly string[] };

export interface OverlayView {
  readonly targetId: string;
  // The gateway-generated STRING id (ObservationTarget.variantId) — the SAME
  // id apps/web/src/screens/ProductDetail.tsx resolves against
  // (`targetsQuery.data.items.find(tg => tg.variantId === variantId)`) and
  // the id `/product?variantId=` deep links expect. This is DISTINCT from
  // `nativeVariantId`/`nativeProductId` (DK's own numeric ids) — never
  // interchange them (EXT-008: a deep link built from the wrong id space
  // resolves nothing).
  readonly variantId: string;
  readonly offerCount: number;
  readonly sellerCount: number;
  /**
   * Raw evidence only — never a Money, and never a cross-unit ordering. See
   * {@link LowestQualifying}: `single` only when every qualifying offer shares
   * one source unit; `conflicted` when incompatible units are present; `none`
   * when nothing qualifies (issue #157).
   */
  readonly lowestQualifying: LowestQualifying;
  readonly freshness: FreshnessBucket | null;
  readonly quality: components["schemas"]["ObservedOffer"]["quality"] | null;
}

const QUALIFYING_AVAILABILITY = new Set(["in_stock", "limited"]);

// freshnessBucketOf DELEGATES to the shared deadline-driven `freshnessState`,
// passing the offer's OWN freshnessDeadline — exactly what apps/web's
// FreshnessPill derives. No fixed-threshold fork remains for these
// deadline-carrying offers.
export function freshnessBucketOf(offer: ObservedOffer, nowMs: number): FreshnessBucket {
  return freshnessState(offer, nowMs);
}

// deriveOverlayView filters the account's Observed Offers down to the one
// target and renders EXACTLY what the server already computed — never invents
// a value, never fabricates a price for an unavailable offer.
export function deriveOverlayView(
  target: ObservationTarget,
  allOffers: readonly ObservedOffer[],
  nowMs: number,
): OverlayView {
  const offers = allOffers.filter((o) => o.targetId === target.id && !o.endedAt);
  const sellerIds = new Set(offers.map((o) => o.nativeSellerId).filter((s): s is string => !!s));

  const qualifying = offers.filter((o) => QUALIFYING_AVAILABILITY.has(o.availabilityStatus));
  const lowestQualifying = lowestWithinCompatibleUnit(qualifying);

  // "Representative" offer for freshness/quality is the most-recently captured
  // one — the same row Market.tsx's `offerByTarget` map keeps (first-wins over
  // an already-ordered list from the server).
  const primary = offers[0] ?? null;

  return {
    targetId: target.id,
    variantId: target.variantId,
    offerCount: offers.length,
    sellerCount: sellerIds.size,
    lowestQualifying,
    freshness: primary ? freshnessBucketOf(primary, nowMs) : null,
    quality: primary?.quality ?? null,
  };
}

// lowestWithinCompatibleUnit orders qualifying offers ONLY when every one shares
// a single source-unit token (issue #157). Compatibility is EXACT token identity
// — there is no verified unit mapping/canonicalization yet (Gate 0a pending), so
// any difference, including a blank/absent token (which stays quarantined and is
// NEVER inferred, RawAmount contract / PRD §9.1), makes two amounts incomparable
// and yields an explicit `conflicted`. No client-authored conversion, ever.
function lowestWithinCompatibleUnit(qualifying: readonly ObservedOffer[]): LowestQualifying {
  if (qualifying.length === 0) return { kind: "none" };

  const units = [...new Set(qualifying.map((o) => o.price.unit))].sort();

  // More than one distinct token, or a single blank/whitespace token (unknown
  // unit), cannot be ordered — quarantine over inference.
  const soleUnit = units.length === 1 ? units[0] : undefined;
  if (soleUnit === undefined || soleUnit.trim() === "") {
    return { kind: "conflicted", units };
  }

  // Every qualifying offer shares one unit: compare raw digit-strings via BigInt
  // (never float/Number — PRD §9.1 no-float-on-money-path posture).
  const lowest = qualifying.reduce((best, o) =>
    bigIntOf(o.price.value) < bigIntOf(best.price.value) ? o : best,
  );
  return { kind: "single", amount: lowest.price };
}

function bigIntOf(raw: string): bigint {
  // Raw price evidence is a digit-string (docs/11); a non-numeric value never
  // silently wins a "lowest" comparison — it sorts last (quarantine posture).
  try {
    return BigInt(raw);
  } catch {
    return BigInt(Number.MAX_SAFE_INTEGER) * 1_000_000n;
  }
}
