import { t } from "../lib/i18n";
import type { FreshnessBucket, ObservedOffer, OverlayView } from "../lib/overlay-data";

// Stable lookup maps (never a dynamically-built catalog key — a template like
// `freshness.${x}` would defeat TypeScript's closed-key coverage and biome's
// literal-key checks) from the extension package's OWN domain values onto the
// SAME catalog keys the SPA badges use (design glossary parity).
const FRESHNESS_KEY: Record<
  FreshnessBucket,
  "freshness.fresh" | "freshness.aging" | "freshness.stale"
> = {
  fresh: "freshness.fresh",
  aging: "freshness.aging",
  stale: "freshness.stale",
};

const QUALITY_KEY: Record<ObservedOffer["quality"], `state.${ObservedOffer["quality"]}`> = {
  verified: "state.verified",
  supported: "state.supported",
  unverified: "state.unverified",
  conflicted: "state.conflicted",
  stale: "state.stale",
  unavailable: "state.unavailable",
};

// The overlay (EXT-010): the ONLY DOM effect the extension has on a Digikala
// page is ONE host element carrying a shadow root, appended once to
// <body>. Nothing else on the page is ever read-mutated, no navigation is
// ever triggered programmatically, no click/keypress/form-submit is ever
// synthesized against the page. Interactive controls inside the overlay
// (refresh / add-to-watchlist) only ever POST A MESSAGE to the service
// worker on a REAL user click — the extension never calls them itself.
//
// EXT-005: values are RENDERED here, never recomputed — the overlay renders
// exactly the `OverlayView` (or an honest degraded/pending state) it is given.

const HOST_ID = "market-ops-overlay-host";

export type OverlayState =
  | { kind: "pending" }
  | { kind: "unavailable" }
  | { kind: "ready"; view: OverlayView };

export interface OverlayActions {
  onRefresh: () => void;
  onAddToWatchlist: () => void;
}

// mountOverlay creates (or reuses) the single overlay host + shadow root.
// Idempotent: calling it twice never appends a second host element.
export function mountOverlay(doc: Document = document): ShadowRoot {
  const existing = doc.getElementById(HOST_ID);
  if (existing) return existing.shadowRoot ?? existing.attachShadow({ mode: "open" });

  const host = doc.createElement("div");
  host.id = HOST_ID;
  doc.body.appendChild(host);
  return host.attachShadow({ mode: "open" });
}

export function unmountOverlay(doc: Document = document): void {
  doc.getElementById(HOST_ID)?.remove();
}

// renderOverlay is a PURE DOM-read-only render into the overlay's OWN shadow
// root — it never touches any node outside that shadow root.
export function renderOverlay(
  root: ShadowRoot,
  state: OverlayState,
  actions: OverlayActions,
): void {
  root.replaceChildren();

  const panel = root.ownerDocument.createElement("div");
  panel.dataset.role = "market-ops-overlay";

  const title = root.ownerDocument.createElement("strong");
  title.dataset.role = "overlay-title";
  title.textContent = t("ext.overlay.title");
  panel.appendChild(title);

  if (state.kind === "pending") {
    panel.appendChild(textRow("overlay-state", t("state.loading")));
  } else if (state.kind === "unavailable") {
    panel.appendChild(textRow("overlay-state", t("ext.overlay.readPending")));
  } else {
    const { view } = state;
    panel.appendChild(textRow("offers", `${t("ext.overlay.offers")}: ${String(view.offerCount)}`));
    panel.appendChild(
      textRow("sellers", `${t("ext.overlay.sellers")}: ${String(view.sellerCount)}`),
    );
    panel.appendChild(
      textRow(
        "lowest",
        `${t("ext.overlay.lowestQualifying")}: ${view.lowestQualifying?.text ?? t("common.notAvailable")}`,
      ),
    );
    panel.appendChild(
      textRow(
        "freshness",
        view.freshness ? t(FRESHNESS_KEY[view.freshness]) : t("common.notAvailable"),
      ),
    );
    panel.appendChild(
      textRow("quality", view.quality ? t(QUALITY_KEY[view.quality]) : t("common.notAvailable")),
    );
  }

  const refreshBtn = actionButton(
    root.ownerDocument,
    t("ext.onDemand.refresh"),
    "on-demand",
    actions.onRefresh,
  );
  const watchlistBtn = actionButton(
    root.ownerDocument,
    t("ext.watchlist.add"),
    "add-watchlist",
    actions.onAddToWatchlist,
  );
  panel.appendChild(refreshBtn);
  panel.appendChild(watchlistBtn);

  root.appendChild(panel);
}

function textRow(role: string, text: string): HTMLElement {
  const el = document.createElement("div");
  el.dataset.role = role;
  el.textContent = text;
  return el;
}

// actionButton NEVER auto-fires — it only calls `onClick` in response to a
// REAL user click event (EXT-010: no synthesized/automated interaction).
function actionButton(
  doc: Document,
  label: string,
  role: string,
  onClick: () => void,
): HTMLButtonElement {
  const b = doc.createElement("button");
  b.type = "button";
  b.dataset.role = role;
  b.textContent = label;
  b.addEventListener("click", onClick);
  return b;
}
