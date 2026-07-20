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

/** The 0-based row index encoded in a makeRows variantId (its last uuid group). */
function indexOf(variantId: string): number {
  return Number(variantId.split("-").at(-1));
}

/**
 * A cursor-paginating `/catalog/products` handler over `rows`, returning `size`
 * rows per page with the last row's nativeVariantId as the opaque cursor. Exercises
 * the BOUNDED client-side page walk (the screen accumulates every page).
 */
function installPaginatedCatalog(rows: CatalogProductRow[], size: number) {
  server.use(
    http.get(`${BASE}/catalog/products`, ({ request }) => {
      const cursor = new URL(request.url).searchParams.get("cursor");
      const start = cursor ? rows.findIndex((r) => String(r.nativeVariantId) === cursor) + 1 : 0;
      const slice = rows.slice(start, start + size);
      const nextCursor =
        start + size < rows.length ? String(slice[slice.length - 1]?.nativeVariantId) : null;
      return HttpResponse.json({ items: slice, nextCursor });
    }),
  );
}

/** The SKU (nativeVariantId) text of every row currently rendered in the table. */
function visibleSkuIds(): string[] {
  const table = document.querySelector(".data-table") as HTMLElement;
  return within(table)
    .queryAllByText(/^500\d{4}$/)
    .map((el) => el.textContent ?? "");
}

describe("Products workspace", () => {
  it("renders canonical rows with mapping state, watched flag, and a deterministic market price", async () => {
    installReadiness();
    renderRoute("/products");

    // "Watched" is an unambiguous marker of the canonical row rendering (the row is
    // NOT an observation target). Await it, then assert the rest.
    await screen.findByText(faIR["mapping.watched"], undefined, { timeout: 5000 });
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
    await screen.findByText("a-seller", undefined, { timeout: 5000 });
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

    await screen.findByText(faIR["mapping.unmapped"], undefined, { timeout: 5000 });
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["mapping.unmapped"])).toBeInTheDocument();
    expect(within(table).getByText(faIR["mapping.unwatched"])).toBeInTheDocument();
  });

  it("applies the readiness filter across the FULL set before paginating (51 of 120)", async () => {
    // 51 of 120 variants are Complete, spread across the whole set (even indices
    // 0..100). The catalog is served in bounded 40-row server pages; the screen
    // walks them, filters to the 51, then paginates the FILTERED set client-side.
    const rows = makeRows(120);
    const isMatch = (i: number) => i % 2 === 0 && i <= 100;
    const matchIds = rows.filter((_, i) => isMatch(i)).map((r) => String(r.nativeVariantId));
    expect(matchIds).toHaveLength(51);

    let readinessCalls = 0;
    installPaginatedCatalog(rows, 40);
    server.use(
      http.get(`${BASE}/cost/readiness`, ({ request }) => {
        readinessCalls += 1;
        const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
        const complete = isMatch(indexOf(variantId));
        return HttpResponse.json({
          ...readinessMissing,
          variantId,
          state: complete ? "complete" : "missing",
          missingComponents: complete ? [] : ["cogs"],
        });
      }),
    );
    renderRoute("/products");

    await screen.findByText("5000000", undefined, { timeout: 10000 });
    fireEvent.click(screen.getByRole("button", { name: faIR["readiness.complete"] }));

    // Count binds to the readiness-filtered set (51), not the loaded page.
    await screen.findByText(faIR["products.count"].replace("{count}", "۵۱"), undefined, {
      timeout: 10000,
    });
    // 51 rows over PAGE_SIZE=20 → 3 pages.
    const indicator = screen.getByTestId("products-page-indicator");
    expect(indicator.textContent).toBe(
      faIR["products.pagination.page"].replace("{page}", "۱").replace("{total}", "۳"),
    );

    // The union of row ids across all pages equals the 51 matches, each exactly once.
    const seen: string[] = [];
    seen.push(...visibleSkuIds());
    expect(visibleSkuIds()).toHaveLength(20);

    fireEvent.click(screen.getByTestId("products-next-page"));
    await waitFor(() =>
      expect(screen.getByTestId("products-page-indicator").textContent).toBe(
        faIR["products.pagination.page"].replace("{page}", "۲").replace("{total}", "۳"),
      ),
    );
    seen.push(...visibleSkuIds());

    fireEvent.click(screen.getByTestId("products-next-page"));
    await waitFor(() =>
      expect(screen.getByTestId("products-page-indicator").textContent).toBe(
        faIR["products.pagination.page"].replace("{page}", "۳").replace("{total}", "۳"),
      ),
    );
    seen.push(...visibleSkuIds());
    // Terminal page: Next disabled, only 11 rows.
    expect(screen.getByTestId("products-next-page")).toBeDisabled();
    expect(visibleSkuIds()).toHaveLength(11);

    expect(seen).toHaveLength(51);
    expect(new Set(seen)).toEqual(new Set(matchIds));
    // Readiness work stays within the documented bound (CATALOG_PAGE_LIMIT = 200),
    // one request per candidate — never whole-catalog-per-page fan-out.
    expect(readinessCalls).toBeLessThanOrEqual(200);
    expect(readinessCalls).toBe(120);
  }, 20000);

  it("bounds readiness fan-out to the visible page when no readiness filter is active", async () => {
    // 120 products, no filter: only the current page's readiness is fetched
    // (≤ PAGE_SIZE), never the whole catalog.
    const rows = makeRows(120);
    let readinessCalls = 0;
    installPaginatedCatalog(rows, 40);
    server.use(
      http.get(`${BASE}/cost/readiness`, ({ request }) => {
        readinessCalls += 1;
        const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
        return HttpResponse.json({ ...readinessMissing, variantId });
      }),
    );
    renderRoute("/products");

    await screen.findByText("5000000", undefined, { timeout: 10000 });
    await waitFor(() => expect(readinessCalls).toBe(20), { timeout: 10000 });
    // Full 120-product set is paginated at 20/page → 6 pages, all describing the set.
    expect(screen.getByTestId("products-page-indicator").textContent).toBe(
      faIR["products.pagination.page"].replace("{page}", "۱").replace("{total}", "۶"),
    );
    expect(readinessCalls).toBe(20);
  }, 20000);

  it("keeps degraded rows and retries only the failed readiness under an active filter", async () => {
    // Filter = Complete. i0 Complete (match), i1 fails (unknown — NOT a definitive
    // mismatch, kept degraded), i2 Missing (definitive mismatch, excluded).
    const rows = makeRows(3);
    const calls = [0, 0, 0];
    installPaginatedCatalog(rows, 50);
    server.use(
      http.get(`${BASE}/cost/readiness`, ({ request }) => {
        const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
        const i = indexOf(variantId);
        calls[i] = (calls[i] ?? 0) + 1;
        if (i === 1 && calls[1] === 1) return new HttpResponse(null, { status: 500 });
        const complete = i === 0 || i === 1;
        return HttpResponse.json({
          ...readinessMissing,
          variantId,
          state: complete ? "complete" : "missing",
          missingComponents: complete ? [] : ["cogs"],
        });
      }),
    );
    renderRoute("/products");

    await screen.findByText("5000000", undefined, { timeout: 10000 });
    fireEvent.click(screen.getByRole("button", { name: faIR["readiness.complete"] }));

    // The successful match and the degraded row are BOTH shown; the definitive
    // mismatch is excluded.
    await waitFor(() => expect(visibleSkuIds().sort()).toEqual(["5000000", "5000001"]), {
      timeout: 10000,
    });
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["products.readiness.unknown"])).toBeInTheDocument();

    // Section error offers retry.
    const sectionError = await screen.findByTestId("products-readiness-error", undefined, {
      timeout: 10000,
    });
    fireEvent.click(within(sectionError).getByRole("button", { name: faIR["action.retry"] }));

    // Retry resolves the failed row to Complete; it stays visible, degraded clears,
    // and only the failed variant was refetched.
    await waitFor(
      () => expect(screen.queryByTestId("products-readiness-error")).not.toBeInTheDocument(),
      { timeout: 10000 },
    );
    expect(within(table).queryByText(faIR["products.readiness.unknown"])).toBeNull();
    expect(visibleSkuIds().sort()).toEqual(["5000000", "5000001"]);
    expect(calls).toEqual([1, 2, 1]);
  }, 20000);

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
    await screen.findByText("5000001", undefined, { timeout: 5000 });
    expect(screen.getByText("5000000")).toBeInTheDocument();
    const sectionError = await screen.findByTestId("products-readiness-error", undefined, {
      timeout: 5000,
    });
    expect(
      within(sectionError).getByRole("button", { name: faIR["action.retry"] }),
    ).toBeInTheDocument();
  });
});
