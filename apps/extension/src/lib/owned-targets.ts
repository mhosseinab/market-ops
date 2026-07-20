import type { ParsedProduct } from "./types";

// EXT-004 (identity quarantine): ONLY Confirmed owned products enter commercial
// data paths. A product whose identity is `Needs Review`, rejected, or simply
// not one of the account's Confirmed owned targets must NEVER be uploaded into
// owned commercial data. Confirmed-owned recognition is SERVER-AUTHORITATIVE: the
// server maps a native variant id to an observation target only for a Confirmed
// identity (OBS-001), and the extension gates on that mapping.
//
// The index is the extension's local projection of the server's Confirmed owned
// targets. It is fail-closed: an empty or unknown index yields NO target, so
// nothing is uploaded until the server has confirmed ownership. Populating this
// index from the authenticated owned-targets read is completed in S31
// (watchlist/targets sync); in S30 it starts EMPTY (Unknown), which by
// construction uploads nothing for an unconfirmed product.

export interface OwnedTarget {
  targetId: string;
  marketplaceAccountId: string;
  nativeVariantId: number;
  // The Confirmed variant identity (CAT-002) the target observes — a UUID, not
  // the DK-native id. Carried so the EXT-007 watchlist add can send the
  // server's WatchlistAddRequest.variantId rather than a guessed id.
  variantId: string;
}

export class OwnedTargetIndex {
  private byVariant = new Map<number, OwnedTarget>();

  // replaceAll swaps the whole index atomically (the server is authoritative;
  // the extension never merges partial owned sets that could resurrect a stale
  // or de-confirmed mapping).
  replaceAll(targets: OwnedTarget[]): void {
    this.byVariant = new Map(targets.map((t) => [t.nativeVariantId, t]));
  }

  // resolve returns the Confirmed owned target for a product's current offer, or
  // null when the product is NOT a Confirmed owned target (Needs Review, rejected,
  // unmapped, or unavailable with no offer). Null means "do not upload" — the
  // EXT-004 gate.
  resolve(product: ParsedProduct): OwnedTarget | null {
    if (product.offer === null) return null;
    return this.byVariant.get(product.offer.nativeVariantId) ?? null;
  }

  get size(): number {
    return this.byVariant.size;
  }
}
