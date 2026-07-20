import type { components } from "@market-ops/gen-ts";

// Watchlist client (EXT-007: add a Confirmed owned product to the priority
// watchlist; the SERVER enforces the cap; the change is audited). The dedicated
// gateway endpoint is OWNED by S37 (api_data_contracts [C], "PD-3 Option 1"):
// S37 landed GET/POST /watchlist in gen/ts (workspace:*, read-only to this
// package), so this seam now issues the REAL credential-scoped call instead of
// the fail-closed stub it carried while S37 was in flight.
//
// The wire shapes come from the generated contract — the extension consumes
// them read-only; a shape mismatch is escalated to api_data_contracts, never
// patched here.
type WatchlistEntry = components["schemas"]["WatchlistEntry"];
type WatchlistView = components["schemas"]["WatchlistView"];
type WatchlistAddRequest = components["schemas"]["WatchlistAddRequest"];

// Fetcher mirrors gateway.ts's injectable transport so the real network call
// stays testable and the default binds the extension's own fetch.
export type Fetcher = (input: string, init?: RequestInit) => Promise<Response>;

export type WatchlistOutcome =
  | { ok: true; entryId: string }
  // `denied` = the server rejected the add (cap reached, an unconfirmed
  // variant, or an unauthorized caller) — the extension NEVER self-certifies the
  // cap; `endpoint_unavailable` = a network failure or an unparseable response,
  // both of which fail closed rather than fabricate a success.
  | { ok: false; reason: "denied" | "endpoint_unavailable" };

export type WatchlistListOutcome =
  | { ok: true; view: WatchlistView }
  | { ok: false; reason: "denied" | "endpoint_unavailable" };

export interface WatchlistRequest {
  // The scoped capture credential (EXT-001) — the server derives the caller's
  // marketplace account from it; no session/cookie is ever attached.
  readonly credential: string;
  readonly marketplaceAccountId: string;
  // The Confirmed owned variant identity (CAT-002) — resolved server-side from
  // the owned-target sync (EXT-004), never a DK-native id.
  readonly variantId: string;
}

export interface WatchlistGateway {
  // addToWatchlist POSTs one Confirmed owned product to the priority watchlist.
  // It NEVER fabricates a success and NEVER self-certifies the cap — a rejection
  // or a network failure fails closed.
  addToWatchlist(req: WatchlistRequest): Promise<WatchlistOutcome>;
  // listWatchlist GETs the account's priority watchlist (a read; fails closed).
  listWatchlist(credential: string, marketplaceAccountId: string): Promise<WatchlistListOutcome>;
}

// HttpWatchlistGateway is the real S37-backed implementation. It follows the
// exact discipline of gateway.ts's other credential-scoped calls: Bearer the
// capture credential, map a non-200 to a fail-closed outcome, and treat a
// network error or an unparseable body as fail-closed — never a guessed success.
class HttpWatchlistGateway implements WatchlistGateway {
  constructor(
    private readonly baseUrl: string,
    private readonly fetcher: Fetcher,
  ) {}

  async addToWatchlist(req: WatchlistRequest): Promise<WatchlistOutcome> {
    const body: WatchlistAddRequest = {
      marketplaceAccountId: req.marketplaceAccountId,
      variantId: req.variantId,
    };
    let resp: Response;
    try {
      resp = await this.fetcher(`${this.baseUrl}/watchlist`, {
        method: "POST",
        headers: {
          "content-type": "application/json",
          authorization: `Bearer ${req.credential}`,
        },
        body: JSON.stringify(body),
      });
    } catch {
      return { ok: false, reason: "endpoint_unavailable" }; // network — fail closed
    }
    // Any non-200 (cap exceeded, unconfirmed variant, revoked/unauthorized) is a
    // server rejection: the extension surfaces `denied`, never a self-certified add.
    if (resp.status !== 200) return { ok: false, reason: "denied" };
    let entry: WatchlistEntry;
    try {
      entry = (await resp.json()) as WatchlistEntry;
    } catch {
      return { ok: false, reason: "endpoint_unavailable" };
    }
    if (!entry || typeof entry.id !== "string") {
      return { ok: false, reason: "endpoint_unavailable" };
    }
    return { ok: true, entryId: entry.id };
  }

  async listWatchlist(
    credential: string,
    marketplaceAccountId: string,
  ): Promise<WatchlistListOutcome> {
    let resp: Response;
    try {
      resp = await this.fetcher(
        `${this.baseUrl}/watchlist?marketplaceAccountId=${encodeURIComponent(marketplaceAccountId)}`,
        { method: "GET", headers: { authorization: `Bearer ${credential}` } },
      );
    } catch {
      return { ok: false, reason: "endpoint_unavailable" }; // network — fail closed
    }
    if (resp.status !== 200) return { ok: false, reason: "denied" };
    let view: WatchlistView;
    try {
      view = (await resp.json()) as WatchlistView;
    } catch {
      return { ok: false, reason: "endpoint_unavailable" };
    }
    if (!view || !Array.isArray(view.items)) {
      return { ok: false, reason: "endpoint_unavailable" };
    }
    return { ok: true, view };
  }
}

// createWatchlistGateway is the SINGLE construction point the service worker
// calls. It builds the real S37-backed client; the default fetcher binds the
// extension's own fetch (host-scoped to the gateway origin at deploy — no new
// manifest permission), while tests inject a fake fetcher.
export function createWatchlistGateway(
  baseUrl: string,
  fetcher: Fetcher = globalThis.fetch.bind(globalThis),
): WatchlistGateway {
  return new HttpWatchlistGateway(baseUrl, fetcher);
}
