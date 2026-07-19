import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import type { CatalogProductRow, ObservedOffer } from "../data/types";
import { catalogProductRow, offer, readinessMissing } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

/** N canonical product rows with distinct ids and native identifiers. */
function makeRows(n: number, base = 0): CatalogProductRow[] {
  return Array.from({ length: n }, (_, i) => ({
    ...catalogProductRow,
    variantId: `00000000-0000-0000-0000-${String(base + i).padStart(12, "0")}`,
    nativeVariantId: 5000000 + base + i,
    nativeProductId: 9000000 + base + i,
    marketOffers: [],
  }));
}

/** Variant-aware readiness handler returning Missing for every variant. */
function installReadiness() {
  server.use(
    http.get(`${BASE}/cost/readiness`, ({ request }) => {
      const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
      return HttpResponse.json({ ...readinessMissing, variantId });
    }),
  );
}

describe("Products workspace", () => {
  it("renders canonical rows with mapping state, watched flag, and a deterministic market price", async () => {
    installReadiness();
    renderRoute("/products");

    // "Watched" is an unambiguous marker of the canonical row rendering (the row is
    // NOT an observation target). Await it, then assert the rest.
    await screen.findByText(faIR["mapping.watched"]);
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["mapping.watched"])).toBeInTheDocument();
    // Confirmed mapping badge + Verified quality badge share the Persian glossary
    // word "تاییدشده", so both render it: at least two occurrences in the row.
    expect(within(table).getAllByText(faIR["mapping.confirmed"]).length).toBeGreaterThanOrEqual(2);
    // Market price cell shows the offer identity AND its raw price (money quarantine).
    expect(within(table).getByText("8842213:seller-1")).toBeInTheDocument();
    expect(within(table).getByText("14,350,000")).toBeInTheDocument();
    // Native IDs render as RAW LTR identifiers (fa-IR default): no grouping/conversion.
    expect(within(table).getByText("7719004")).toBeInTheDocument();
    expect(within(table).getByText("8842213")).toBeInTheDocument();
    expect(within(table).queryByText("۷٬۷۱۹٬۰۰۴")).toBeNull();
    // Bulk entry point present.
    expect(screen.getByTestId("bulk-entry")).toBeInTheDocument();
  });

  it("surfaces multiple competitor offers individually with identity, in deterministic order", async () => {
    installReadiness();
    // The read model already orders offers by offerIdentity ascending, so the FIRST
    // shown is 'a-seller' regardless of input order.
    const offers: ObservedOffer[] = [
      { ...offer, offerIdentity: "a-seller", price: { text: "10", value: "10", unit: "IRR" } },
      { ...offer, offerIdentity: "z-seller", price: { text: "99", value: "99", unit: "IRR" } },
    ];
    server.use(
      http.get(`${BASE}/catalog/products`, () =>
        HttpResponse.json({ items: [{ ...catalogProductRow, marketOffers: offers }] }),
      ),
    );
    renderRoute("/products");

    // The primary (identity-labelled) offer is the first ordered one, with a count.
    await screen.findByText("a-seller");
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText("a-seller")).toBeInTheDocument();
    expect(within(table).getByText("10")).toBeInTheDocument();
    expect(
      within(table).getByText(faIR["products.market.multiple"].replace("{count}", "۲")),
    ).toBeInTheDocument();
  });

  it("shows unmapped and unwatched states for synced variants without an active target", async () => {
    installReadiness();
    server.use(
      http.get(`${BASE}/catalog/products`, () =>
        HttpResponse.json({
          items: [
            {
              ...catalogProductRow,
              variantId: "aaaaaaaa-0000-0000-0000-000000000001",
              nativeVariantId: 1,
              mappingState: "unmapped",
              watched: false,
              marketOffers: [],
            },
          ],
        }),
      ),
    );
    renderRoute("/products");

    await screen.findByText(faIR["mapping.unmapped"]);
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["mapping.unmapped"])).toBeInTheDocument();
    expect(within(table).getByText(faIR["mapping.unwatched"])).toBeInTheDocument();
  });

  it("advances pages via the server cursor", async () => {
    installReadiness();
    let call = 0;
    server.use(
      http.get(`${BASE}/catalog/products`, ({ request }) => {
        const cursor = new URL(request.url).searchParams.get("cursor");
        call += 1;
        if (!cursor) {
          // First page: full, with a next cursor.
          return HttpResponse.json({ items: makeRows(1, 0), nextCursor: "5000000" });
        }
        // Second page: terminal, no next cursor.
        return HttpResponse.json({ items: makeRows(1, 60), nextCursor: null });
      }),
    );
    renderRoute("/products");

    await screen.findByText("5000000"); // page-1 native variant id
    expect(screen.queryByText("5000060")).toBeNull();

    fireEvent.click(screen.getByTestId("products-next-page"));

    await screen.findByText("5000060"); // page-2 native variant id
    expect(screen.queryByText("5000000")).toBeNull();
    await waitFor(() => expect(call).toBeGreaterThanOrEqual(2));
    // At the terminal page the Next button is disabled (no next cursor).
    expect(screen.getByTestId("products-next-page")).toBeDisabled();
  });

  it("shows an actionable section error on partial readiness failure without dropping rows", async () => {
    server.use(
      http.get(`${BASE}/catalog/products`, () =>
        HttpResponse.json({
          items: [
            {
              ...catalogProductRow,
              variantId: "00000000-0000-0000-0000-000000000000",
              nativeVariantId: 5000000,
              marketOffers: [],
            },
            {
              ...catalogProductRow,
              variantId: "00000000-0000-0000-0000-000000000001",
              nativeVariantId: 5000001,
              marketOffers: [],
            },
          ],
        }),
      ),
      http.get(`${BASE}/cost/readiness`, ({ request }) => {
        const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
        if (variantId === "00000000-0000-0000-0000-000000000000") {
          return new HttpResponse(null, { status: 500 });
        }
        return HttpResponse.json({ ...readinessMissing, variantId });
      }),
    );
    renderRoute("/products");

    // Both rows still render — the table is not thrown away.
    await screen.findByText("5000001");
    expect(screen.getByText("5000000")).toBeInTheDocument();
    const sectionError = await screen.findByTestId("products-readiness-error");
    expect(
      within(sectionError).getByRole("button", { name: faIR["action.retry"] }),
    ).toBeInTheDocument();
  });
});
