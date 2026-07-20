import { describe, expect, it } from "vitest";
import available from "../test/fixtures/product-available.json";
import type { Capability } from "./capability";
import { OwnedTargetIndex } from "./owned-targets";
import { parseProductResponse } from "./parse";
import { prepareCapture } from "./pipeline";
import type { ParsedProduct } from "./types";

function parsed(): ParsedProduct {
  const r = parseProductResponse(available);
  if (!r.ok) throw new Error("fixture must parse");
  return r.product;
}

function variantId(product: ParsedProduct): number {
  if (product.offer === null) throw new Error("fixture must have an offer");
  return product.offer.nativeVariantId;
}

const account = "11111111-1111-1111-1111-111111111111";
const target = "22222222-2222-2222-2222-222222222222";

function confirmedIndex(nativeVariantId: number): OwnedTargetIndex {
  const idx = new OwnedTargetIndex();
  idx.replaceAll([
    {
      targetId: target,
      marketplaceAccountId: account,
      nativeVariantId,
      variantId: "44444444-4444-4444-4444-444444444444",
    },
  ]);
  return idx;
}

describe("prepareCapture — EXT-004 + capability gates (never-cut)", () => {
  it("enqueues a capture for a Confirmed owned target when capability is ready", () => {
    const product = parsed();
    const idx = confirmedIndex(variantId(product));
    const d = prepareCapture(product, idx, "ready", "2026-07-18T10:00:00Z");
    expect(d.action).toBe("enqueue");
    if (d.action !== "enqueue") return;
    expect(d.capture.targetId).toBe(target);
    expect(d.capture.marketplaceAccountId).toBe(account);
    expect(d.capture.subRoute).toBe("passive");
  });

  it("NEVER uploads a product that is not a Confirmed owned target (Needs Review / unmapped)", () => {
    const product = parsed();
    // The owned index does NOT contain this variant → NeedsReview/unmapped.
    const emptyIdx = new OwnedTargetIndex();
    const d = prepareCapture(product, emptyIdx, "ready", "2026-07-18T10:00:00Z");
    expect(d).toEqual({ action: "skip", reason: "not_confirmed_owned" });
  });

  it("Unknown capability never enables capture (Unknown never enables)", () => {
    const product = parsed();
    const idx = confirmedIndex(variantId(product));
    for (const cap of ["unknown", "revoked", "disabled"] as Capability[]) {
      const d = prepareCapture(product, idx, cap, "2026-07-18T10:00:00Z");
      expect(d).toEqual({ action: "skip", reason: `capability_${cap}` });
    }
  });

  it("attributes the sub-route the extension actually used (OBS-005) — on-demand vs watchlist", () => {
    const product = parsed();
    const idx = confirmedIndex(variantId(product));
    const onDemand = prepareCapture(product, idx, "ready", "2026-07-18T10:00:00Z", "on_demand");
    if (onDemand.action !== "enqueue") throw new Error("expected enqueue");
    expect(onDemand.capture.subRoute).toBe("on_demand");

    const watchlist = prepareCapture(product, idx, "ready", "2026-07-18T10:00:00Z", "watchlist");
    if (watchlist.action !== "enqueue") throw new Error("expected enqueue");
    expect(watchlist.capture.subRoute).toBe("watchlist");
  });
});
