import type { HistorySeries } from "./history";
import type { OverlayView } from "./overlay-data";
import type { OwnedTarget } from "./owned-targets";
import type { PopupState } from "./storage";
import type { ParsedProduct } from "./types";
import type { WatchlistOutcome } from "./watchlist";

// Typed message envelope between the content script / popup and the service
// worker. Keeping it explicit means the worker never trusts an untyped payload.
export type ExtMessage =
  | { kind: "capture"; product: ParsedProduct }
  // EXT-003: on-demand refresh for the CURRENT product only — bypasses the
  // 1-minute alarm hint and flushes immediately (bounded ≤10s).
  | { kind: "onDemandCapture"; product: ParsedProduct }
  // EXT-007: add a Confirmed owned target to the priority watchlist. Resolved
  // through the SAME Confirmed-owned-target gate as capture — a
  // NeedsReview/unmapped product can never reach the watchlist (EXT-004).
  | { kind: "addToWatchlist"; product: ParsedProduct }
  // EXT-012: user opt-in toggle for bounded scheduled refresh.
  | { kind: "setScheduleEnabled"; enabled: boolean }
  // EXT-005 (fail-closed pending captureAuth read-scope widening — S31 CF):
  // request the overlay's current view for the product being viewed. Resolved
  // through the SAME Confirmed-owned-target gate as capture (EXT-004) — the
  // overlay never shows data for a NeedsReview/unmapped product either.
  | { kind: "getOverlayView"; product: ParsedProduct }
  // Diagnostic-only page-context injection (docs/09: "scripting: optional
  // diagnostic interception only") — the MAIN-world nav shim (S31 carry-forward
  // fix). The content script asks the service worker to inject it into ITS OWN
  // tab (the service worker resolves the tab id from the sender, never from an
  // enumerated/other tab).
  | { kind: "injectNavShim" }
  | { kind: "pair"; code: string }
  | { kind: "setEnabled"; enabled: boolean }
  | { kind: "revoke" }
  | { kind: "getState" }
  | { kind: "setOwnedTargets"; targets: OwnedTarget[] };

export type ExtResponse =
  | { ok: true; state: PopupState }
  | { ok: true; watchlist: WatchlistOutcome }
  | {
      ok: true;
      overlay:
        | { kind: "pending" }
        | { kind: "unavailable" }
        | { kind: "ready"; view: OverlayView; history: HistorySeries | null };
    }
  | { ok: true }
  | { ok: false; error: string };
