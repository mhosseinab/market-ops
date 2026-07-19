import { waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { describe, expect, it } from "vitest";
import type { ConnectorStatus } from "../data/types";
import { ACCOUNT_ID, connectorUnknown } from "../test/msw/fixtures";
import { BASE } from "../test/msw/handlers";
import { server } from "../test/msw/server";
import { renderRoute } from "../test/renderRoute";

// Issue #18: the TopBar pill is wired to the CURRENT typed connector status and
// fails closed. These render the FULL app so the query/derive/pill seam is
// exercised exactly as production, then assert the pill tone for the account's
// real connector state.

const ALL_CAPS = [
  "catalog_read",
  "owned_offer_read",
  "stock_read",
  "buybox_read",
  "boundary_read",
  "commission_read",
  "sales_context_read",
  "price_write",
  "change_feed",
] as const;

function overrideStatus(status: ConnectorStatus) {
  server.use(http.get(`${BASE}/connector/status`, () => HttpResponse.json(status)));
}

function findPill() {
  return waitFor(() => {
    const pill = document.querySelector<HTMLElement>(".connection-pill");
    if (!pill) throw new Error("pill not rendered");
    return pill;
  });
}

describe("TopBar connector pill (issue #18, fail closed)", () => {
  it("renders a non-positive pill for the default disconnected account", async () => {
    overrideStatus(connectorUnknown);
    renderRoute("/today");
    const pill = await findPill();
    await waitFor(() => expect(pill.getAttribute("data-health")).toBe("disconnected"));
    expect(pill.className).not.toContain("tone-pos");
  });

  it("never reads healthy while only some capabilities are probed (fail closed)", async () => {
    overrideStatus({
      marketplaceAccountId: ACCOUNT_ID,
      connectionState: "connected",
      capabilities: ALL_CAPS.map((capability, i) => ({
        capability,
        status: i === 0 ? "supported" : "unknown",
      })),
    });
    renderRoute("/today");
    const pill = await findPill();
    await waitFor(() => expect(pill.getAttribute("data-health")).toBe("probing"));
    expect(pill.className).not.toContain("tone-pos");
  });

  it("reads positive ONLY when connected and every capability is confirmed supported", async () => {
    overrideStatus({
      marketplaceAccountId: ACCOUNT_ID,
      connectionState: "connected",
      capabilities: ALL_CAPS.map((capability) => ({ capability, status: "supported" })),
    });
    renderRoute("/today");
    const pill = await findPill();
    await waitFor(() => expect(pill.getAttribute("data-health")).toBe("supported"));
    expect(pill.className).toContain("tone-pos");
    expect(pill.getAttribute("role")).toBe("status");
  });
});
