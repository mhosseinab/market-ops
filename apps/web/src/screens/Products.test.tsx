import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { ACCOUNT_ID, readinessMissing } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

// The PAGE_SIZE the screen bounds each readiness fan-out to (mirrors the module
// constant in Products.tsx). Kept here so the test asserts the real bound.
const PAGE_SIZE = 50;

/** N deterministic observation targets with distinct ids for query-key isolation. */
function makeTargets(n: number) {
  return Array.from({ length: n }, (_, i) => ({
    id: `t-${i}`,
    marketplaceAccountId: ACCOUNT_ID,
    identityId: `id-${i}`,
    // Distinct variantId per target so each readiness query has its own key.
    variantId: `00000000-0000-0000-0000-${String(i).padStart(12, "0")}`,
    // Distinct, sequential native IDs — the LTR search haystack + visibility marker.
    nativeVariantId: 5000000 + i,
    nativeProductId: 9000000 + i,
    tier: "standard" as const,
    cadenceSeconds: 3600,
    freshnessDeadlineSeconds: 3600,
    active: true,
  }));
}

/**
 * Install a variant-aware `/cost/readiness` handler that COUNTS requests, so a
 * test can prove the per-row fan-out is BOUNDED (page-sized), not one-per-target.
 * `failFor` lets a test force a partial batch failure on specific variantIds.
 */
function countingReadiness(failFor: (variantId: string) => boolean = () => false) {
  const counter = { count: 0 };
  server.use(
    http.get(`${BASE}/cost/readiness`, ({ request }) => {
      counter.count += 1;
      const variantId = new URL(request.url).searchParams.get("variantId") ?? "";
      if (failFor(variantId)) return new HttpResponse(null, { status: 500 });
      return HttpResponse.json({ ...readinessMissing, variantId });
    }),
  );
  return counter;
}

describe("Products workspace", () => {
  it("renders readiness + quality badges and the bulk entry point", async () => {
    renderRoute("/products");

    // Market quality (Verified) from the observed offer.
    expect(await screen.findByText(faIR["state.verified"])).toBeInTheDocument();
    // Margin readiness (Missing) — scoped to the table (it is also a filter chip).
    const table = document.querySelector(".data-table") as HTMLElement;
    expect(within(table).getByText(faIR["readiness.missing"])).toBeInTheDocument();
    // Bulk entry stub deep-links to the (S28) bulk screen.
    expect(screen.getByTestId("bulk-entry")).toBeInTheDocument();

    // Native IDs render as RAW LTR identifiers (fa-IR default): no thousands
    // separators and no Persian-digit conversion — they must match DK verbatim.
    expect(within(table).getByText("7719004")).toBeInTheDocument(); // nativeProductId
    expect(within(table).getByText("8842213")).toBeInTheDocument(); // nativeVariantId
    expect(within(table).queryByText("7,719,004")).toBeNull(); // never grouped
    expect(within(table).queryByText("۷٬۷۱۹٬۰۰۴")).toBeNull(); // never Persian digits
  });

  it("bounds readiness fan-out to one page even with many targets (#75)", async () => {
    server.use(
      http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: makeTargets(120) })),
    );
    const readiness = countingReadiness();
    renderRoute("/products");

    // Wait for the table (first page) to render.
    await screen.findByText("5000000"); // page-1 target 0 nativeVariantId

    // The fan-out is BOUNDED: at most one page of readiness calls, never 120.
    await waitFor(() => expect(readiness.count).toBeGreaterThan(0));
    expect(readiness.count).toBeLessThanOrEqual(PAGE_SIZE);
    // A target on a later page must NOT have been fetched on initial render.
    expect(screen.queryByText("5000060")).toBeNull(); // page-2 target 60
  });

  it("fetches only the next batch when advancing a page (#75)", async () => {
    server.use(
      http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: makeTargets(120) })),
    );
    const readiness = countingReadiness();
    renderRoute("/products");

    await screen.findByText("5000000"); // page 1 visible
    await waitFor(() => expect(readiness.count).toBeGreaterThan(0));
    const afterPage1 = readiness.count;
    expect(afterPage1).toBeLessThanOrEqual(PAGE_SIZE);

    // Advance to page 2.
    fireEvent.click(screen.getByTestId("products-next-page"));

    // Page-2 target becomes visible; page-1-only target leaves the table.
    await screen.findByText("5000050"); // page 2 target 50
    expect(screen.queryByText("5000000")).toBeNull();

    // Only the next batch was fetched — the increase is page-bounded, not 120.
    await waitFor(() => expect(readiness.count).toBeGreaterThan(afterPage1));
    expect(readiness.count - afterPage1).toBeLessThanOrEqual(PAGE_SIZE);
  });

  it("shows an actionable section error on partial readiness failure without dropping rows (#75)", async () => {
    server.use(
      http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: makeTargets(10) })),
    );
    // Fail readiness for target 0 only; the rest succeed.
    const failedVariant = `00000000-0000-0000-0000-${String(0).padStart(12, "0")}`;
    countingReadiness((variantId) => variantId === failedVariant);
    renderRoute("/products");

    // The successful rows still render — the table is not thrown away.
    await screen.findByText("5000001"); // target 1 (succeeded) visible
    expect(screen.getByText("5000000")).toBeInTheDocument(); // failed row still shown

    // An actionable section-level error with a retry affordance is present.
    const sectionError = await screen.findByTestId("products-readiness-error");
    expect(sectionError).toBeInTheDocument();
    expect(
      within(sectionError).getByRole("button", { name: faIR["action.retry"] }),
    ).toBeInTheDocument();
  });
});
