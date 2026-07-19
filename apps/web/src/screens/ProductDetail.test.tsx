import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { catalogProductRow, VARIANT_ID } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Product detail", () => {
  it("renders the Supported owned offer, market snapshot, mapping state, and missing-cost banner", async () => {
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    // Owned offer renders raw price evidence when the capability is Supported —
    // never the unconditional "unavailable" text.
    expect(await screen.findByText("14,000,000")).toBeInTheDocument();
    expect(screen.queryByText(faIR["product.ownedOffer.unavailable"])).toBeNull();
    expect(screen.queryByText(faIR["product.ownedOffer.reason.capabilityNotSupported"])).toBeNull();
    // Mapping state badge (Confirmed) from the canonical row.
    expect(screen.getAllByText(faIR["mapping.confirmed"]).length).toBeGreaterThan(0);
    // Readiness = Missing → the missing-cost blocked banner (design screen 8).
    expect(screen.getAllByText(faIR["readiness.missing"]).length).toBeGreaterThan(0);
    // With no cost profile rows, every component reads "not recorded".
    expect(screen.getAllByText(faIR["product.cost.notRecorded"]).length).toBeGreaterThan(0);
    // Market snapshot renders the raw price evidence (money quarantine — not Money).
    expect(screen.getByText("14,350,000")).toBeInTheDocument();
    // Native product ID is a RAW LTR identifier (fa-IR default).
    expect(screen.getByText("7719004")).toBeInTheDocument();
    expect(screen.queryByText("۷٬۷۱۹٬۰۰۴")).toBeNull();
  });

  it("shows a reason (never a fabricated price) when owned_offer_read is not Supported", async () => {
    server.use(
      http.get(`${BASE}/catalog/product`, () =>
        HttpResponse.json({
          ...catalogProductRow,
          mappingState: "needs_review",
          watched: false,
          ownedOffer: {
            capability: "unknown",
            present: false,
            unavailableReason: "capability_not_supported",
          },
        }),
      ),
    );
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    // The gated reason renders; the owned price is NOT shown (Unknown never enables).
    expect(
      await screen.findByText(faIR["product.ownedOffer.reason.capabilityNotSupported"]),
    ).toBeInTheDocument();
    expect(screen.queryByText("14,000,000")).toBeNull();
    // The capability badge surfaces the Unknown state.
    expect(screen.getAllByText(faIR["capabilityState.unknown"]).length).toBeGreaterThan(0);
    // Mapping state reflects Needs review.
    expect(screen.getAllByText(faIR["mapping.needsReview"]).length).toBeGreaterThan(0);
  });
});
