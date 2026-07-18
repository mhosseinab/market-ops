import type { Observation } from "./history";

// History read gateway (EXT-006 data source), mirroring overlay-read.ts's
// discipline EXACTLY: the extension's only credential (captureAuth) is scoped
// server-side to `/observation/capture` alone, so it cannot read
// `/observation/observations` today either. This is a SIBLING fail-closed
// seam (not a change to overlay-read.ts's existing contract) — same reasoning,
// same named downstream (a future contracts step widening captureAuth's read
// scope, or a dedicated read credential).

export type HistoryReadOutcome =
  | { ok: true; observations: readonly Observation[]; gapThresholdSeconds: number }
  | { ok: false; reason: "endpoint_unavailable" | "denied" };

export interface HistoryReadGateway {
  fetchHistory(targetId: string): Promise<HistoryReadOutcome>;
}

export const pendingHistoryReadGateway: HistoryReadGateway = {
  async fetchHistory(): Promise<HistoryReadOutcome> {
    return { ok: false, reason: "endpoint_unavailable" };
  },
};

// createHistoryReadGateway is the single swap point (mirrors watchlist.ts /
// overlay-read.ts).
export function createHistoryReadGateway(): HistoryReadGateway {
  return pendingHistoryReadGateway;
}
