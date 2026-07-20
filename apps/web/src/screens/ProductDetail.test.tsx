import { faIR } from "@market-ops/locale";
import { screen, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { catalogProductRow, offer, VARIANT_ID } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

// Runtime failures for a SECONDARY query must render as errors, never as a
// business absence — an absence state may appear ONLY after a request COMPLETED
// SUCCESSFULLY with no data (issue #81). Each secondary query is exercised at
// 500/401/403 independently; the containment is status-agnostic (the gateway
// wrapper throws on any error envelope), and the assertion proves both the
// scoped, actionable error AND the absence NOT rendering in that section.
const FAIL_STATUSES = [500, 401, 403] as const;

/** The `section.panel` element whose heading is the given catalog term. */
function panelByHeading(term: string): HTMLElement {
  const heading = screen.getByRole("heading", { name: term });
  const panel = heading.closest("section");
  if (!panel) throw new Error(`no panel for heading ${term}`);
  return panel as HTMLElement;
}

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Product detail", () => {
  it("renders the Supported owned offer, market snapshot, mapping state, and missing-cost banner", async () => {
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    // Owned offer renders raw price evidence when the capability is Supported —
    // never the unconditional "unavailable" text.
    expect(await screen.findByText("14,000,000", undefined, { timeout: 5000 })).toBeInTheDocument();
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
      await screen.findByText(faIR["product.ownedOffer.reason.capabilityNotSupported"], undefined, {
        timeout: 5000,
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText("14,000,000")).toBeNull();
    // The capability badge surfaces the Unknown state.
    expect(screen.getAllByText(faIR["capabilityState.unknown"]).length).toBeGreaterThan(0);
    // Mapping state reflects Needs review.
    expect(screen.getAllByText(faIR["mapping.needsReview"]).length).toBeGreaterThan(0);
  });
});

describe("Product detail — secondary query failures never become business absence (#81)", () => {
  for (const status of FAIL_STATUSES) {
    it(`renders an actionable readiness error, not a readiness absence, on ${status}`, async () => {
      server.use(http.get(`${BASE}/cost/readiness`, () => new HttpResponse(null, { status })));
      renderRoute(`/product?variantId=${VARIANT_ID}`);

      // The canonical product still loads (owned price present).
      await screen.findByText("14,000,000", undefined, { timeout: 5000 });

      // The readiness section shows a scoped, actionable error + retry — never the
      // legitimate "Not available" absence.
      const err = await screen.findByTestId("product-readiness-error", undefined, {
        timeout: 5000,
      });
      expect(within(err).getByRole("button", { name: faIR["action.retry"] })).toBeInTheDocument();
      const readinessPanel = panelByHeading(faIR["product.section.readiness"]);
      expect(within(readinessPanel).queryByText(faIR["common.notAvailable"])).toBeNull();

      // The Missing-cost banner (a readiness-derived absence) must NOT appear on failure.
      const contribPanel = panelByHeading(faIR["product.contribution.title"]);
      expect(within(contribPanel).getByTestId("product-contribution-error")).toBeInTheDocument();
      expect(within(contribPanel).queryByText(faIR["product.contribution.placeholder"])).toBeNull();
    });

    it(`renders an actionable cost error, never "Not recorded", on ${status}`, async () => {
      server.use(http.get(`${BASE}/cost/profiles`, () => new HttpResponse(null, { status })));
      renderRoute(`/product?variantId=${VARIANT_ID}`);

      await screen.findByText("14,000,000", undefined, { timeout: 5000 });
      const err = await screen.findByTestId("product-cost-error", undefined, { timeout: 5000 });
      expect(within(err).getByRole("button", { name: faIR["action.retry"] })).toBeInTheDocument();
      // "Not recorded" is a SUCCESSFUL-empty disposition; a failed request never shows it.
      expect(screen.queryByText(faIR["product.cost.notRecorded"])).toBeNull();
    });

    it(`renders an actionable diagnostics error, not an absence, when diagnostics fail on ${status}`, async () => {
      // The diagnostics panel is sourced from the REAL listing/image diagnostics
      // endpoint (LST-001) — never from observation capture provenance.
      server.use(
        http.get(`${BASE}/catalog/product-diagnostics`, () => new HttpResponse(null, { status })),
      );
      renderRoute(`/product?variantId=${VARIANT_ID}`);

      await screen.findByText("14,000,000", undefined, { timeout: 5000 });
      const err = await screen.findByTestId("product-diagnostics-error", undefined, {
        timeout: 5000,
      });
      expect(within(err).getByRole("button", { name: faIR["action.retry"] })).toBeInTheDocument();
      const diagPanel = panelByHeading(faIR["product.section.diagnostics"]);
      expect(within(diagPanel).queryByText(faIR["common.notAvailable"])).toBeNull();
    });

    it(`surfaces the whole-screen error (offers never render as absence) when the product query fails on ${status}`, async () => {
      server.use(http.get(`${BASE}/catalog/product`, () => new HttpResponse(null, { status })));
      renderRoute(`/product?variantId=${VARIANT_ID}`);

      // The market snapshot is sourced from the product row; on its failure the
      // ViewState error takes the whole screen — never an empty market snapshot.
      expect(
        await screen.findByText(faIR["state.error.title"], undefined, { timeout: 5000 }),
      ).toBeInTheDocument();
      expect(screen.queryByText(faIR["product.marketPrice"])).toBeNull();
    });
  }

  it('retains "Not recorded" for a SUCCESSFUL empty cost-profiles response (empty != error)', async () => {
    server.use(http.get(`${BASE}/cost/profiles`, () => HttpResponse.json({ items: [] })));
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    // A completed empty response is a legitimate absence: "Not recorded" per component.
    expect(
      (await screen.findAllByText(faIR["product.cost.notRecorded"], undefined, { timeout: 5000 }))
        .length,
    ).toBeGreaterThan(0);
    expect(screen.queryByTestId("product-cost-error")).toBeNull();
  });

  it("retains the diagnostics absence for a SUCCESSFUL empty diagnostics report (empty != error)", async () => {
    server.use(
      http.get(`${BASE}/catalog/product-diagnostics`, () =>
        HttpResponse.json({
          variantId: VARIANT_ID,
          marketplaceAccountId: "00000000-0000-0000-0000-000000000003",
          evaluatedAt: "2026-07-19T12:00:00Z",
          items: [],
        }),
      ),
    );
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    await screen.findByText("14,000,000", undefined, { timeout: 5000 });
    const diagPanel = panelByHeading(faIR["product.section.diagnostics"]);
    expect(within(diagPanel).getByText(faIR["common.notAvailable"])).toBeInTheDocument();
    expect(screen.queryByTestId("product-diagnostics-error")).toBeNull();
  });

  it("retains the market-snapshot absence for a SUCCESSFUL product with no offers (empty != error)", async () => {
    server.use(
      http.get(`${BASE}/catalog/product`, () =>
        HttpResponse.json({ ...catalogProductRow, marketOffers: [] }),
      ),
    );
    renderRoute(`/product?variantId=${VARIANT_ID}`);

    await screen.findByText("14,000,000", undefined, { timeout: 5000 });
    const snapshotPanel = panelByHeading(faIR["product.section.snapshot"]);
    expect(within(snapshotPanel).getByText(faIR["common.notAvailable"])).toBeInTheDocument();
  });

  // A guard against a false-positive: `offer` is imported so the fixture stays in
  // sync with the snapshot assertions above.
  it("keeps the deterministic primary offer identity available to the snapshot", () => {
    expect(offer.offerIdentity).toBe("8842213:seller-1");
  });
});
