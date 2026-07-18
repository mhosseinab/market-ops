import { describe, expect, it } from "vitest";
import { createWatchlistGateway, pendingS37WatchlistGateway } from "./watchlist";

// EXT-007's dedicated endpoint is owned by S37 (still in flight when S31
// shipped). This is the negative test proving the seam FAILS CLOSED — it never
// fabricates a watchlist success, and never self-certifies a cap the server
// alone enforces.
describe("watchlist gateway — fail-closed seam pending S37 (EXT-007)", () => {
  it("NEVER claims success — addToWatchlist always fails closed until S37 lands", async () => {
    const outcome = await pendingS37WatchlistGateway.addToWatchlist({
      marketplaceAccountId: "acct-1",
      targetId: "target-1",
    });
    expect(outcome).toEqual({ ok: false, reason: "endpoint_unavailable" });
  });

  it("createWatchlistGateway is the single swap point and currently returns the fail-closed stub", () => {
    expect(createWatchlistGateway()).toBe(pendingS37WatchlistGateway);
  });

  it("issues NO network call (pure fail-closed stub, no fetch reference)", async () => {
    const gw = createWatchlistGateway();
    // No fetcher was ever injected into this gateway — there is nothing to spy
    // on because it structurally cannot reach the network.
    const result = await gw.addToWatchlist({ marketplaceAccountId: "a", targetId: "b" });
    expect(result.ok).toBe(false);
  });
});
