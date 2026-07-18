import type { Capability } from "../lib/capability";
import { buildDeepLink } from "../lib/deeplink";
import type { HistorySeries } from "../lib/history";
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
// worker on a REAL user click — the extension never calls them itself — and
// are gated on capability === "ready" (Unknown/disabled/revoked never enables
// dependent UI, PRD §4.6, mirroring popup.ts's schedule-toggle pattern).
//
// EXT-005: values are RENDERED here, never recomputed — the overlay renders
// exactly the `OverlayView` (or an honest degraded/pending state) it is given.
//
// LOC boundary (LOC-005): the DK product page is RTL. Every raw/technical
// evidence token (raw price text, native ids) is rendered inside an
// LTR-isolated span (`direction:ltr; unicode-bidi:isolate`) — the same
// posture as apps/web's LtrToken — so it never bidi-scrambles.

const HOST_ID = "market-ops-overlay-host";

const OVERLAY_STYLE = `
  .market-ops-overlay {
    direction: rtl;
    font: 12px/1.6 system-ui, sans-serif;
    background: #1c1c1ecc;
    color: #fff;
    padding: 8px 10px;
    border-radius: 6px;
    max-width: 260px;
  }
  .market-ops-ltr {
    direction: ltr;
    unicode-bidi: isolate;
    display: inline-block;
  }
  .market-ops-overlay button {
    margin-inline-end: 6px;
    margin-block-start: 6px;
  }
  .market-ops-history-gap {
    opacity: 0.7;
    font-style: italic;
  }
`;

export type OverlayState =
  | { kind: "pending" }
  | { kind: "unavailable" }
  | { kind: "ready"; view: OverlayView; history: HistorySeries | null };

export interface OverlayActions {
  onRefresh: () => void;
  onAddToWatchlist: () => void;
}

// Everything renderOverlay needs that is NOT server data: the current
// capability, for action-button gating.
export interface OverlayContext {
  readonly capability: Capability;
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

// ltrToken wraps a raw/technical evidence string (price text, native id) in an
// isolated LTR span — mirroring apps/web's LtrToken — so it never bidi-
// scrambles inside the RTL overlay panel.
function ltrToken(doc: Document, role: string, text: string): HTMLElement {
  const el = doc.createElement("span");
  el.dataset.role = role;
  el.className = "market-ops-ltr";
  el.textContent = text;
  return el;
}

// renderOverlay is a PURE DOM-read-only render into the overlay's OWN shadow
// root — it never touches any node outside that shadow root.
export function renderOverlay(
  root: ShadowRoot,
  state: OverlayState,
  actions: OverlayActions,
  context: OverlayContext,
): void {
  root.replaceChildren();

  const style = root.ownerDocument.createElement("style");
  style.textContent = OVERLAY_STYLE;
  root.appendChild(style);

  const panel = root.ownerDocument.createElement("div");
  panel.className = "market-ops-overlay";
  panel.dataset.role = "market-ops-overlay";

  const title = root.ownerDocument.createElement("strong");
  title.dataset.role = "overlay-title";
  title.textContent = t("ext.overlay.title");
  panel.appendChild(title);

  if (state.kind === "pending") {
    panel.appendChild(textRow(root.ownerDocument, "overlay-state", t("state.loading")));
  } else if (state.kind === "unavailable") {
    panel.appendChild(textRow(root.ownerDocument, "overlay-state", t("ext.overlay.readPending")));
  } else {
    const { view } = state;
    panel.appendChild(
      textRow(
        root.ownerDocument,
        "offers",
        `${t("ext.overlay.offers")}: ${String(view.offerCount)}`,
      ),
    );
    panel.appendChild(
      textRow(
        root.ownerDocument,
        "sellers",
        `${t("ext.overlay.sellers")}: ${String(view.sellerCount)}`,
      ),
    );
    panel.appendChild(lowestQualifyingRow(root.ownerDocument, view));
    panel.appendChild(
      textRow(
        root.ownerDocument,
        "freshness",
        view.freshness ? t(FRESHNESS_KEY[view.freshness]) : t("common.notAvailable"),
      ),
    );
    panel.appendChild(
      textRow(
        root.ownerDocument,
        "quality",
        view.quality ? t(QUALITY_KEY[view.quality]) : t("common.notAvailable"),
      ),
    );
    panel.appendChild(renderHistory(root.ownerDocument, state.history));

    // EXT-008: a real, user-clicked deep link to the product's context in the
    // web app (ordinary browser navigation via <a target="_blank">, never an
    // automated navigation of the CURRENT Digikala page — EXT-010 governs the
    // content script's effect on digikala.com, not opening a new tab). Built
    // ONLY from `view.variantId` — the gateway-generated STRING id
    // ProductDetail.tsx actually resolves against — NEVER from DK's own
    // numeric nativeProductId/nativeVariantId (a different id space; a link
    // built from the wrong space would silently resolve nothing). Rendered
    // ONLY once we have the real variantId (state.kind === "ready") — no
    // link is ever shown built from a guessed/wrong id.
    panel.appendChild(deepLinkChip(root.ownerDocument, view.variantId));
  }

  // Action buttons are gated on capability === "ready" — Unknown (never
  // paired) / disabled / revoked renders NEITHER button (PRD §4.6: Unknown
  // never enables dependent UI), mirroring popup.ts's schedule-toggle gate.
  if (context.capability === "ready") {
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
  }

  root.appendChild(panel);
}

function lowestQualifyingRow(doc: Document, view: OverlayView): HTMLElement {
  const el = doc.createElement("div");
  el.dataset.role = "lowest";
  el.append(`${t("ext.overlay.lowestQualifying")}: `);
  el.append(
    view.lowestQualifying
      ? ltrToken(doc, "lowest-value", view.lowestQualifying.text)
      : doc.createTextNode(t("common.notAvailable")),
  );
  return el;
}

// renderHistory (EXT-006): a MINIMAL gap-preserving render. Each contiguous
// segment renders its points' raw price text (LTR-isolated); a real gap
// between two evidence captures renders an EXPLICIT gap marker — it NEVER
// draws/interpolates a synthetic point across the gap.
function renderHistory(doc: Document, history: HistorySeries | null): HTMLElement {
  const wrap = doc.createElement("div");
  wrap.dataset.role = "history";

  const heading = doc.createElement("div");
  heading.dataset.role = "history-title";
  heading.textContent = t("ext.history.title");
  wrap.appendChild(heading);

  if (!history || history.segments.length === 0) {
    wrap.appendChild(textRow(doc, "history-empty", t("common.notAvailable")));
    return wrap;
  }

  history.segments.forEach((segment, i) => {
    if (i > 0) {
      const gap = doc.createElement("div");
      gap.dataset.role = "history-gap";
      gap.className = "market-ops-history-gap";
      gap.textContent = t("ext.history.gap");
      wrap.appendChild(gap);
    }
    const seg = doc.createElement("div");
    seg.dataset.role = "history-segment";
    for (const point of segment.points) {
      seg.appendChild(ltrToken(doc, "history-point", `${point.priceValue} ${point.priceUnit}`));
    }
    wrap.appendChild(seg);
  });

  return wrap;
}

function deepLinkChip(doc: Document, variantId: string): HTMLAnchorElement {
  const a = doc.createElement("a");
  a.dataset.role = "deep-link-product";
  a.href = buildDeepLink({ kind: "product", id: variantId });
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  a.textContent = t("ext.deepLink.product");
  return a;
}

function textRow(doc: Document, role: string, text: string): HTMLElement {
  const el = doc.createElement("div");
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
