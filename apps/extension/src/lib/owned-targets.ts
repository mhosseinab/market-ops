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

// OwnedTargetOwner identifies WHICH credential/sync installed the current
// projection (issue #253). It carries the monotonic sync/lifecycle generation
// that produced the projection and the marketplace account that owns it, so a
// stale owned-target sync (completing after a revoke or a re-pair with a
// different credential) can never be mistaken for the live projection. It holds
// NO credential secret — only the account identity and generation number.
export interface OwnedTargetOwner {
  generation: number;
  marketplaceAccountId: string | null;
}

const DETACHED_OWNER: OwnedTargetOwner = { generation: 0, marketplaceAccountId: null };

export class OwnedTargetIndex {
  private byVariant = new Map<number, OwnedTarget>();
  // The identity/generation the CURRENT projection belongs to. Every atomic
  // replaceAll re-stamps it, so the installed set is always self-describing about
  // which credential/sync owns it (identity quarantine, EXT-004).
  private currentOwner: OwnedTargetOwner = DETACHED_OWNER;

  // replaceAll swaps the whole index atomically (the server is authoritative;
  // the extension never merges partial owned sets that could resurrect a stale
  // or de-confirmed mapping) and stamps the projection with the owner
  // (credential-account identity + sync generation) that installed it.
  replaceAll(targets: OwnedTarget[], owner: OwnedTargetOwner = DETACHED_OWNER): void {
    this.byVariant = new Map(targets.map((t) => [t.nativeVariantId, t]));
    this.currentOwner = owner;
  }

  // owner exposes the identity/generation of the installed projection so callers
  // can assert the live set belongs to the expected credential (never a stale one).
  get owner(): OwnedTargetOwner {
    return this.currentOwner;
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
