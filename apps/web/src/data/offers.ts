import type { ObservedOffer } from "./types";

// Grouping the account's Observed Offers by their target, preserving EVERY offer
// identity (OBS-004 / evidence-quality never-cut). The read model returns all
// current offers per (target, offer_identity) ordered only by `updated_at`; a
// target may legitimately carry MULTIPLE offer identities, so collapsing to one
// arbitrary row per target both hides a conflicted/stale sibling and lets an
// unrelated timestamp change the chosen offer. This groups without dropping any
// offer and sorts each target's offers by their OWN stable identity — offer
// identity then id — so the rendered/classified result is ORDER-INDEPENDENT:
// reordering `updated_at` never changes what is shown or how it is classified.
export function offersByTargetId(
  offers: readonly ObservedOffer[],
): Map<string, ObservedOffer[]> {
  const map = new Map<string, ObservedOffer[]>();
  for (const o of offers) {
    const list = map.get(o.targetId);
    if (list) list.push(o);
    else map.set(o.targetId, [o]);
  }
  for (const list of map.values()) list.sort(compareOfferIdentity);
  return map;
}

// Deterministic, timestamp-independent order for a target's offers.
function compareOfferIdentity(a: ObservedOffer, b: ObservedOffer): number {
  if (a.offerIdentity !== b.offerIdentity)
    return a.offerIdentity < b.offerIdentity ? -1 : 1;
  if (a.id !== b.id) return a.id < b.id ? -1 : 1;
  return 0;
}

// A stable, per-offer row key: an offer renders under its own id; a target with
// no observed offer keeps a single placeholder row under the target id.
export function offerRowKey(targetId: string, offer?: ObservedOffer): string {
  return offer ? `offer:${offer.id}` : `target:${targetId}`;
}
