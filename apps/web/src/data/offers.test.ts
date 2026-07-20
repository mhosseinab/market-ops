import { describe, expect, it } from "vitest";
import { offer } from "../test/msw/fixtures";
import { offerRowKey, offersByTargetId } from "./offers";
import type { ObservedOffer } from "./types";

function make(id: string, targetId: string, offerIdentity: string, extra?: Partial<ObservedOffer>) {
  return { ...offer, id, targetId, offerIdentity, ...extra } satisfies ObservedOffer;
}

describe("offersByTargetId (OBS-004: preserve every offer identity, order-independent)", () => {
  it("keeps EVERY offer identity on a target — never collapses to the first row", () => {
    const a = make("o-a", "t-1", "8842213:seller-1", { quality: "verified" });
    const b = make("o-b", "t-1", "8842213:seller-2", { quality: "conflicted" });
    const grouped = offersByTargetId([a, b]);
    const list = grouped.get("t-1");
    expect(list).toHaveLength(2);
    expect(list?.map((o) => o.offerIdentity).sort()).toEqual([
      "8842213:seller-1",
      "8842213:seller-2",
    ]);
    // The conflicted sibling is present with its OWN quality — not averaged away.
    expect(list?.find((o) => o.id === "o-b")?.quality).toBe("conflicted");
  });

  it("is ORDER-INDEPENDENT — reordering the input never changes the grouped result", () => {
    const a = make("o-a", "t-1", "8842213:seller-1");
    const b = make("o-b", "t-1", "8842213:seller-2");
    const c = make("o-c", "t-2", "9000000:seller-1");
    const forward = offersByTargetId([a, b, c]);
    const reversed = offersByTargetId([c, b, a]);
    const norm = (m: Map<string, ObservedOffer[]>) =>
      [...m.entries()]
        .map(([k, v]) => [k, v.map((o) => o.id)])
        .sort((x, y) => (x[0] < y[0] ? -1 : 1));
    expect(norm(forward)).toEqual(norm(reversed));
    expect(forward.get("t-1")?.map((o) => o.id)).toEqual(["o-a", "o-b"]);
  });

  it("gives an offer its own key and a target-only placeholder key", () => {
    const a = make("o-a", "t-1", "id-1");
    expect(offerRowKey("t-1", a)).toBe("offer:o-a");
    expect(offerRowKey("t-1", undefined)).toBe("target:t-1");
  });
});
