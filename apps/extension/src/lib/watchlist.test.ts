import { describe, expect, it, vi } from "vitest";
import { createWatchlistGateway } from "./watchlist";

// EXT-007's dedicated endpoint landed in S37 (commit 75c826f: GET/POST
// /watchlist in gen/ts). The gateway now issues the REAL credential-scoped call
// — but it MUST still fail closed: a network failure or a server rejection
// never fabricates a watchlist success and never self-certifies the cap (the
// SERVER alone enforces it). These tests pin all three paths: success, server
// rejection, and network-fail-closed.

const ACCOUNT = "11111111-1111-1111-1111-111111111111";
const VARIANT = "cccccccc-cccc-cccc-cccc-cccccccccccc";
const CRED = "cap-cred-hex";

function entry() {
  return {
    id: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee",
    marketplaceAccountId: ACCOUNT,
    variantId: VARIANT,
    createdAt: "2026-07-18T10:00:00Z",
  };
}

function view() {
  return { marketplaceAccountId: ACCOUNT, cap: 50, items: [entry()] };
}

describe("watchlist gateway — real S37 POST /watchlist (EXT-007)", () => {
  // network-fail-closed FIRST (the acceptance criteria's headline invariant).
  it("fails closed (endpoint_unavailable) on a network failure — never a fabricated success", async () => {
    const gw = createWatchlistGateway("http://gw", async () => {
      throw new Error("offline");
    });
    const outcome = await gw.addToWatchlist({
      credential: CRED,
      marketplaceAccountId: ACCOUNT,
      variantId: VARIANT,
    });
    expect(outcome).toEqual({ ok: false, reason: "endpoint_unavailable" });
  });

  it("returns the entry id on a 200 and sends the credential as a Bearer + the WatchlistAddRequest body", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify(entry()), { status: 200 }));
    const gw = createWatchlistGateway("http://gw", fetcher);
    const outcome = await gw.addToWatchlist({
      credential: CRED,
      marketplaceAccountId: ACCOUNT,
      variantId: VARIANT,
    });
    expect(outcome).toEqual({ ok: true, entryId: entry().id });
    expect(fetcher).toHaveBeenCalledWith(
      "http://gw/watchlist",
      expect.objectContaining({
        method: "POST",
        headers: expect.objectContaining({ authorization: `Bearer ${CRED}` }),
        body: JSON.stringify({ marketplaceAccountId: ACCOUNT, variantId: VARIANT }),
      }),
    );
  });

  it("fails closed (denied) on a server rejection — the server enforces the cap, the extension never self-certifies it", async () => {
    for (const status of [400, 403, 409, 500]) {
      const gw = createWatchlistGateway(
        "http://gw",
        async () => new Response(JSON.stringify({ code: "REJECTED", message: "x" }), { status }),
      );
      const outcome = await gw.addToWatchlist({
        credential: CRED,
        marketplaceAccountId: ACCOUNT,
        variantId: VARIANT,
      });
      expect(outcome).toEqual({ ok: false, reason: "denied" });
    }
  });

  it("fails closed (endpoint_unavailable) when a 200 body is not a WatchlistEntry — never invents an entry id", async () => {
    const gw = createWatchlistGateway(
      "http://gw",
      async () => new Response("not json", { status: 200 }),
    );
    const outcome = await gw.addToWatchlist({
      credential: CRED,
      marketplaceAccountId: ACCOUNT,
      variantId: VARIANT,
    });
    expect(outcome).toEqual({ ok: false, reason: "endpoint_unavailable" });
  });
});

describe("watchlist gateway — real S37 GET /watchlist (EXT-007)", () => {
  it("fails closed (endpoint_unavailable) on a network failure", async () => {
    const gw = createWatchlistGateway("http://gw", async () => {
      throw new Error("offline");
    });
    expect(await gw.listWatchlist(CRED, ACCOUNT)).toEqual({
      ok: false,
      reason: "endpoint_unavailable",
    });
  });

  it("returns the WatchlistView on a 200 and sends the credential as a Bearer + the account query", async () => {
    const fetcher = vi.fn(async () => new Response(JSON.stringify(view()), { status: 200 }));
    const gw = createWatchlistGateway("http://gw", fetcher);
    const outcome = await gw.listWatchlist(CRED, ACCOUNT);
    expect(outcome).toEqual({ ok: true, view: view() });
    expect(fetcher).toHaveBeenCalledWith(
      `http://gw/watchlist?marketplaceAccountId=${ACCOUNT}`,
      expect.objectContaining({
        method: "GET",
        headers: expect.objectContaining({ authorization: `Bearer ${CRED}` }),
      }),
    );
  });

  it("fails closed (denied) on a non-200", async () => {
    const gw = createWatchlistGateway("http://gw", async () => new Response("{}", { status: 401 }));
    expect(await gw.listWatchlist(CRED, ACCOUNT)).toEqual({ ok: false, reason: "denied" });
  });

  it("fails closed (endpoint_unavailable) on a malformed 200 body — never a fabricated view", async () => {
    const gw = createWatchlistGateway(
      "http://gw",
      async () => new Response(JSON.stringify({ nope: true }), { status: 200 }),
    );
    expect(await gw.listWatchlist(CRED, ACCOUNT)).toEqual({
      ok: false,
      reason: "endpoint_unavailable",
    });
  });
});
