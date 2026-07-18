import { describe, expect, it } from "vitest";
import { createOverlayReadGateway, pendingOverlayReadGateway } from "./overlay-read";

// Negative test (planned-stub discipline): the overlay read gateway is
// FAIL-CLOSED until captureAuth's read scope is widened beyond
// /observation/capture (a genuine, named, out-of-S31-scope contract gap).
describe("overlay read gateway — fail-closed seam (captureAuth is capture-only today)", () => {
  it("NEVER fabricates overlay data — fails closed with an honest reason", async () => {
    const outcome = await pendingOverlayReadGateway.fetchOverlayData("target-1");
    expect(outcome).toEqual({ ok: false, reason: "endpoint_unavailable" });
  });

  it("createOverlayReadGateway is the single swap point, currently the fail-closed stub", () => {
    expect(createOverlayReadGateway()).toBe(pendingOverlayReadGateway);
  });
});
