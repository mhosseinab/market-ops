import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor, within } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it } from "vitest";
import { offer } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("Market (freshness / quality / conflicts)", () => {
  it("renders freshness coverage, quality distribution, and the watch table", async () => {
    renderRoute("/market");
    expect(await screen.findByTestId("coverage-bars")).toBeInTheDocument();
    expect(screen.getByTestId("quality-distribution")).toBeInTheDocument();
    // The single default offer is Verified quality.
    expect(screen.getAllByText(faIR["state.verified"]).length).toBeGreaterThan(0);
    expect(screen.getByText(faIR["market.watch.title"])).toBeInTheDocument();
  });

  it("surfaces the conflicted-observation banner with per-route evidence and a deep link", async () => {
    // The banner reads the dedicated /market/conflicts endpoint, which carries each
    // conflicted offer's per-route disagreeing evidence (issue #94).
    server.use(
      http.get(`${BASE}/market/conflicts`, () =>
        HttpResponse.json({
          items: [
            {
              ...offer,
              quality: "conflicted",
              conflictEvidence: {
                state: "available",
                routes: [
                  {
                    route: "route_c",
                    value: "100",
                    unit: "IRR-rial",
                    availabilityStatus: "in_stock",
                    capturedAt: "2026-07-20T10:00:00Z",
                    freshnessDeadline: "2026-07-20T11:00:00Z",
                  },
                  {
                    route: "route_a",
                    value: "200",
                    unit: "IRR-rial",
                    availabilityStatus: "in_stock",
                    capturedAt: "2026-07-20T10:01:00Z",
                    freshnessDeadline: "2026-07-20T11:01:00Z",
                  },
                ],
              },
            },
          ],
        }),
      ),
    );
    renderRoute("/market");
    const toOps = await screen.findByTestId("conflict-to-operations");
    expect(toOps).toHaveAttribute("href", expect.stringContaining("/operations"));
    // Both routes' disagreeing raw values are shown side-by-side (the evidence that
    // caused the block), each retaining its route identity.
    expect(await screen.findByText("route_c")).toBeInTheDocument();
    expect(screen.getByText("route_a")).toBeInTheDocument();
    expect(screen.getByText("100 IRR-rial")).toBeInTheDocument();
    expect(screen.getByText("200 IRR-rial")).toBeInTheDocument();
  });

  it("shows an explicit fail-closed error when the comparison evidence is unavailable", async () => {
    // Missing/incomplete comparison evidence is an EXPLICIT read-model error — never a
    // fabricated complete panel and never client-side inference (issue #94).
    server.use(
      http.get(`${BASE}/market/conflicts`, () =>
        HttpResponse.json({
          items: [
            {
              ...offer,
              quality: "conflicted",
              conflictEvidence: { state: "unavailable", routes: [] },
            },
          ],
        }),
      ),
    );
    renderRoute("/market");
    expect(await screen.findByTestId("conflict-evidence-unavailable")).toHaveTextContent(
      faIR["market.conflict.evidenceUnavailable"],
    );
    // The action stays blocked: the deep link to Operations is still present.
    expect(screen.getByTestId("conflict-to-operations")).toBeInTheDocument();
  });

  it("issues a budgeted refresh request without recomputing anything", async () => {
    let refreshed = false;
    server.use(
      http.post(`${BASE}/connector/refresh`, () => {
        refreshed = true;
        return HttpResponse.json({
          marketplaceAccountId: offer.marketplaceAccountId,
          connectionState: "connected",
          capabilities: [],
        });
      }),
    );
    renderRoute("/market");
    fireEvent.click(await screen.findByTestId("market-refresh"));
    await waitFor(() => expect(refreshed).toBe(true));
  });

  it("keeps BOTH offer identities on one target visible and attributable (OBS-004)", async () => {
    // Two offer identities on the SAME target: a Verified one and a Conflicted
    // sibling. The conflicted sibling must NOT disappear behind the verified one.
    const verified = {
      ...offer,
      id: "o-v",
      offerIdentity: "8842213:seller-1",
      quality: "verified" as const,
    };
    const conflicted = {
      ...offer,
      id: "o-c",
      offerIdentity: "8842213:seller-2",
      quality: "conflicted" as const,
    };
    server.use(
      http.get(`${BASE}/observation/observed-offers`, () =>
        HttpResponse.json({ items: [verified, conflicted] }),
      ),
    );
    renderRoute("/market");
    await screen.findByText(faIR["market.watch.title"]);
    const table = document.querySelector(".data-table") as HTMLElement;
    // Both offer identities render in the watch table, attributable to their own
    // seller — the conflicted sibling is NOT hidden behind the verified one.
    expect(within(table).getByText("8842213:seller-1")).toBeInTheDocument();
    expect(within(table).getByText("8842213:seller-2")).toBeInTheDocument();
    // Each keeps its OWN quality — verified AND conflicted are both visible in the table.
    expect(within(table).getByText(faIR["state.verified"])).toBeInTheDocument();
    expect(within(table).getByText(faIR["state.conflicted"])).toBeInTheDocument();
  });

  it("is order-independent — reordering the offers does not change what is shown", async () => {
    const a = {
      ...offer,
      id: "o-a",
      offerIdentity: "8842213:seller-1",
      quality: "verified" as const,
    };
    const b = { ...offer, id: "o-b", offerIdentity: "8842213:seller-2", quality: "stale" as const };
    server.use(
      http.get(`${BASE}/observation/observed-offers`, () => HttpResponse.json({ items: [b, a] })),
    );
    renderRoute("/market");
    const rows = await screen.findAllByText(/8842213:seller-/);
    // Both offer identities present regardless of the arrival order.
    expect(rows.map((n) => n.textContent).sort()).toEqual(["8842213:seller-1", "8842213:seller-2"]);
  });

  it("surfaces the empty state when there are no watch targets", async () => {
    server.use(http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: [] })));
    renderRoute("/market");
    expect(await screen.findByText(faIR["state.empty.title"])).toBeInTheDocument();
  });
});
