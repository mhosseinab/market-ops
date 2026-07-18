import type { ObservationTarget, ObservedOffer } from "./overlay-data";

// Overlay read gateway (EXT-005 data source). The overlay must render values
// EQUAL to the Market screen, which reads `/observation/targets` +
// `/observation/observed-offers` under the human's session. The extension's
// ONLY credential is the capture credential (EXT-001) — and it is scoped
// server-side to `/observation/capture` alone (gen/ts schema.d.ts: "It
// authorizes ONLY /observation/capture...", confirmed structurally in the S30
// review: captureAuth is consulted ONLY in the kindCapture middleware branch).
// It CANNOT read `/observation/observed-offers` today.
//
// This is a genuine credential-SCOPE gap, not a decision this step can make —
// widening captureAuth's scope (or minting a distinct overlay-read credential)
// is contract/server work outside S31 (NON-[C]: this step may not touch
// contracts). Per CLAUDE.md's stub discipline, this is the thin, clearly-marked,
// FAIL-CLOSED seam: it never fabricates overlay data, and names the downstream
// (a future contracts step widening captureAuth's read scope, or a dedicated
// overlay-read credential) that completes it.

export type OverlayReadOutcome =
  | { ok: true; target: ObservationTarget; offers: readonly ObservedOffer[] }
  | { ok: false; reason: "endpoint_unavailable" | "denied" };

export interface OverlayReadGateway {
  fetchOverlayData(targetId: string): Promise<OverlayReadOutcome>;
}

export const pendingOverlayReadGateway: OverlayReadGateway = {
  async fetchOverlayData(): Promise<OverlayReadOutcome> {
    return { ok: false, reason: "endpoint_unavailable" };
  },
};

// createOverlayReadGateway is the single swap point (mirrors watchlist.ts):
// once captureAuth's read scope is widened (or a dedicated credential exists),
// only this factory needs to change.
export function createOverlayReadGateway(): OverlayReadGateway {
  return pendingOverlayReadGateway;
}
