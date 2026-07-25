// Attribution of a SERVER-sealed bulk record (a selection-set member view, or a
// bulk-confirmation item result) to the OFFER ROW it belongs to (issue #87).
//
// Every sibling offer on one observation target shares ONE recommendation — there is
// exactly one live control-bearing approval card per variant, and the selection-set
// schema carries UNIQUE (selection_set_id, variant_id). So two sibling offers can
// never be two members of one selection set, and keying a server record by
// recommendation ALONE attributes one member's outcome to EVERY sibling row on that
// target — including a conflicted one, which renders a blocked offer as approved.
// That is the #87 identity collapse resurfacing at the operator's decision surface,
// in the unsafe direction.
//
// The server now seals the offer identity onto both the member view and the item
// result, so the match is (recommendationId, offerIdentity). Two absence rules the
// contract states explicitly and this module implements literally:
//
//   * an EMPTY string is EXPLICIT ABSENCE, never a stand-in for another offer —
//     it is normalized to `undefined` and never matches a real identity;
//   * a record reporting NO identity (an omitted field, or a selection-set version
//     sealed before #87) may only be claimed by a target carrying a SINGLE offer
//     row, where attribution is unambiguous. On a multi-offer target it is claimed
//     by NO row — never broadcast.
//
// Nothing here recomputes authority: it only decides which row renders which
// server-authoritative record.

/** The minimum shape of a server-sealed, per-member record. */
export interface SealedByOffer {
  readonly recommendationId: string;
  readonly offerIdentity?: string;
}

/** The offer row asking which sealed record (if any) is its own. */
export interface OfferRowIdentity {
  readonly recommendationId?: string;
  readonly offerIdentity?: string;
  /** True when the row's target contributes exactly one offer row. */
  readonly soleOfferOnTarget: boolean;
}

/** An empty sealed identity is EXPLICIT ABSENCE, not an identity. */
function presentIdentity(value: string | undefined): string | undefined {
  return value === undefined || value === "" ? undefined : value;
}

/** Group sealed records by recommendation; a target's siblings share one. */
export function indexByRecommendation<T extends SealedByOffer>(
  records: readonly T[],
): Map<string, T[]> {
  const map = new Map<string, T[]>();
  for (const r of records) {
    const list = map.get(r.recommendationId);
    if (list) list.push(r);
    else map.set(r.recommendationId, [r]);
  }
  return map;
}

/**
 * The sealed record belonging to THIS offer row, or `undefined` when the server
 * reported none for it. `undefined` is a real answer — the caller renders the
 * explicit "not in the selection set" node, never an assumed success.
 */
export function findByOfferIdentity<T extends SealedByOffer>(
  index: ReadonlyMap<string, T[]>,
  row: OfferRowIdentity,
): T | undefined {
  if (!row.recommendationId) return undefined;
  const candidates = index.get(row.recommendationId);
  if (!candidates || candidates.length === 0) return undefined;

  const rowIdentity = presentIdentity(row.offerIdentity);
  if (rowIdentity !== undefined) {
    const exact = candidates.find((r) => presentIdentity(r.offerIdentity) === rowIdentity);
    if (exact) return exact;
  }

  // No sealed identity to match on. Only an unambiguous single-offer target may
  // claim the record; on a multi-offer target it belongs to no row.
  if (!row.soleOfferOnTarget) return undefined;
  return candidates.find((r) => presentIdentity(r.offerIdentity) === undefined);
}
