import { describe, expect, it } from "vitest";
import { findByOfferIdentity, indexByRecommendation } from "./bulkResults";
import type { BulkApprovalItemResult, SelectionSetMemberView } from "./types";

const REC = "ffffffff-ffff-ffff-ffff-ffffffffffff";
const VARIANT = "11111111-1111-1111-1111-111111111111";

function item(offerIdentity: string | undefined): BulkApprovalItemResult {
  return {
    variantId: VARIANT,
    recommendationId: REC,
    disposition: "executable",
    state: "authorized",
    reason: "authorized",
    ...(offerIdentity === undefined ? {} : { offerIdentity }),
  };
}

describe("bulk result attribution keyed on the SERVER-sealed offer identity (issue #87, W1)", () => {
  it("W1: does NOT broadcast one member's outcome to a sibling offer on the same target", () => {
    // The #87 defect at the operator's decision surface: one target, two offer
    // identities, ONE selection member. Keying the lookup by recommendation alone
    // attributed the member's `authorized` outcome to BOTH rows — including the
    // conflicted sibling, which the server never authorized.
    const index = indexByRecommendation([item("8842213:seller-1")]);

    const verified = findByOfferIdentity(index, {
      recommendationId: REC,
      offerIdentity: "8842213:seller-1",
      soleOfferOnTarget: false,
    });
    const conflicted = findByOfferIdentity(index, {
      recommendationId: REC,
      offerIdentity: "8842213:seller-2",
      soleOfferOnTarget: false,
    });

    expect(verified?.state).toBe("authorized");
    expect(conflicted).toBeUndefined();
  });

  it("W1: treats an EMPTY sealed identity as explicit absence, never as a match", () => {
    // The contract states an empty string is EXPLICIT ABSENCE, never a stand-in
    // for another offer. It must not match a row that has a real identity.
    const index = indexByRecommendation([item("")]);

    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: "8842213:seller-1",
        soleOfferOnTarget: false,
      }),
    ).toBeUndefined();
  });

  it("W1: falls back to the recommendation match ONLY for a single-offer target", () => {
    // A pre-#87 sealed version reports no identity. A target carrying exactly one
    // offer row is unambiguous, so the row may claim the item; a multi-offer
    // target is ambiguous and no row may claim it (never broadcast).
    const index = indexByRecommendation([item(undefined)]);

    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: "8842213:seller-1",
        soleOfferOnTarget: true,
      })?.state,
    ).toBe("authorized");
    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: "8842213:seller-1",
        soleOfferOnTarget: false,
      }),
    ).toBeUndefined();
    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: "8842213:seller-2",
        soleOfferOnTarget: false,
      }),
    ).toBeUndefined();
  });

  it("W1: a target with NO observed offer still matches an identity-less item (criterion E)", () => {
    const index = indexByRecommendation([item(undefined)]);

    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: undefined,
        soleOfferOnTarget: true,
      })?.state,
    ).toBe("authorized");
  });

  it("W1: a row with no live approval control never matches an item", () => {
    const index = indexByRecommendation([item("8842213:seller-1")]);

    expect(
      findByOfferIdentity(index, {
        recommendationId: undefined,
        offerIdentity: "8842213:seller-1",
        soleOfferOnTarget: true,
      }),
    ).toBeUndefined();
  });

  it("W1: attribution is INDEPENDENT of the order the server reports items in (criterion B)", () => {
    const a = { ...item("8842213:seller-1"), state: "authorized" as const };
    const b = { ...item("8842213:seller-2"), state: "failed" as const };
    const row = {
      recommendationId: REC,
      offerIdentity: "8842213:seller-2",
      soleOfferOnTarget: false,
    };

    expect(findByOfferIdentity(indexByRecommendation([a, b]), row)?.state).toBe("failed");
    expect(findByOfferIdentity(indexByRecommendation([b, a]), row)?.state).toBe("failed");
  });

  it("W1: the same matcher resolves a SEALED SELECTION MEMBER (issue #87, W3)", () => {
    // The identical rule is what attributes the server's downgrade reason to the
    // offer row it belongs to — one matcher, one source of truth (DRY).
    const member: SelectionSetMemberView = {
      variantId: VARIANT,
      recommendationId: REC,
      disposition: "blocked",
      offerIdentity: "8842213:seller-1",
      reason: "target_offer_evidence_unusable",
    };
    const index = indexByRecommendation([member]);

    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: "8842213:seller-1",
        soleOfferOnTarget: false,
      })?.reason,
    ).toBe("target_offer_evidence_unusable");
    expect(
      findByOfferIdentity(index, {
        recommendationId: REC,
        offerIdentity: "8842213:seller-2",
        soleOfferOnTarget: false,
      }),
    ).toBeUndefined();
  });
});
