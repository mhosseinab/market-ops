// Watchlist client (EXT-007: add a Confirmed owned product to the priority
// watchlist; server enforces the cap; the change is audited). The dedicated
// gateway endpoint is OWNED by S37 (api_data_contracts [C], "PD-3 Option 1" —
// docs/implementation/dk-p0-progress.md 2026-07-18 entry), which was still in
// flight when this step shipped. gen/ts (workspace:*, read-only to this
// package) carries NO watchlist schema/operation yet.
//
// Per CLAUDE.md "a step may claim a behavior only after its complete seam is
// wired... explicitly planned stubs fail closed, carry a negative test, and
// name the downstream step that completes them" — this is exactly that: a
// thin, clearly-marked adapter seam. It NEVER fabricates a success, NEVER
// self-certifies a cap, and NEVER invents an endpoint shape that could
// contradict what S37 actually freezes.

export type WatchlistOutcome =
  | { ok: true; auditId: string }
  | { ok: false; reason: "cap_reached" | "denied" | "endpoint_unavailable" };

export interface WatchlistRequest {
  readonly marketplaceAccountId: string;
  readonly targetId: string;
}

export interface WatchlistGateway {
  addToWatchlist(req: WatchlistRequest): Promise<WatchlistOutcome>;
}

// pendingS37WatchlistGateway is the FAIL-CLOSED default: it never claims
// success and never issues a network call, because there is nothing verified
// to call yet. Swapping in the real gateway (once S37 lands the endpoint and
// gen/ts carries its operation) is a one-line change at the call site in
// service-worker.ts — this seam is the only thing that needs to move.
export const pendingS37WatchlistGateway: WatchlistGateway = {
  async addToWatchlist(): Promise<WatchlistOutcome> {
    return { ok: false, reason: "endpoint_unavailable" };
  },
};

// createWatchlistGateway is the SINGLE construction point the service worker
// calls. Today it always returns the fail-closed stub; once S37 merges and
// gen/ts exposes the watchlist operation, this factory is updated to build a
// real GatewayClient-backed implementation — nothing else in this module
// needs to change.
export function createWatchlistGateway(): WatchlistGateway {
  return pendingS37WatchlistGateway;
}
