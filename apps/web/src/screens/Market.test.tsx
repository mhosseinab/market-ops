import { faIR } from "@market-ops/locale";
import { fireEvent, screen, waitFor } from "@testing-library/react";
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

  it("surfaces the conflicted-observation banner with an Operations deep link", async () => {
    server.use(
      http.get(`${BASE}/observation/observed-offers`, () =>
        HttpResponse.json({ items: [{ ...offer, quality: "conflicted" }] }),
      ),
    );
    renderRoute("/market");
    const toOps = await screen.findByTestId("conflict-to-operations");
    expect(toOps).toHaveAttribute("href", expect.stringContaining("/operations"));
    // Cross-route values are not surfaced — explicitly stated, never fabricated.
    expect(screen.getByText(faIR["market.conflict.valuesUnavailable"])).toBeInTheDocument();
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

  it("surfaces the empty state when there are no watch targets", async () => {
    server.use(http.get(`${BASE}/observation/targets`, () => HttpResponse.json({ items: [] })));
    renderRoute("/market");
    expect(await screen.findByText(faIR["state.empty.title"])).toBeInTheDocument();
  });
});
