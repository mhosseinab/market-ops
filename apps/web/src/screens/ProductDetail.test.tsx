import { faIR } from "@market-ops/locale";
import { screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { VARIANT_ID } from "../test/msw/fixtures";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Product detail", () => {
  it("shows owned-offer unavailable, missing-cost banner, and unrecorded cost components", async () => {
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    // Owned offer is explicitly unavailable — never fabricated.
    expect(await screen.findByText(faIR["product.ownedOffer.unavailable"])).toBeInTheDocument();
    // Readiness = Missing → the missing-cost blocked banner (design screen 8).
    expect(screen.getAllByText(faIR["readiness.missing"]).length).toBeGreaterThan(0);
    // With no cost profile rows, every component reads "not recorded".
    expect(screen.getAllByText(faIR["product.cost.notRecorded"]).length).toBeGreaterThan(0);
    // Market snapshot renders the raw price evidence (money quarantine — not Money).
    expect(screen.getByText("14,350,000")).toBeInTheDocument();
  });
});
