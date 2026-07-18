import { describe, expect, it } from "vitest";
import { createHistoryReadGateway, pendingHistoryReadGateway } from "./history-read";

// Negative test (planned-stub discipline, mirrors watchlist.ts/overlay-read.ts):
// history data is FAIL-CLOSED until captureAuth's read scope is widened.
describe("history read gateway — fail-closed seam (captureAuth is capture-only today)", () => {
  it("NEVER fabricates history evidence — fails closed with an honest reason", async () => {
    const outcome = await pendingHistoryReadGateway.fetchHistory("target-1");
    expect(outcome).toEqual({ ok: false, reason: "endpoint_unavailable" });
  });

  it("createHistoryReadGateway is the single swap point, currently the fail-closed stub", () => {
    expect(createHistoryReadGateway()).toBe(pendingHistoryReadGateway);
  });
});
