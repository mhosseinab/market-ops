import { beforeEach, describe, expect, it, vi } from "vitest";
import type { HistorySeries } from "../lib/history";
import type { OverlayView } from "../lib/overlay-data";
import {
  mountOverlay,
  type OverlayContext,
  type OverlayState,
  renderOverlay,
  unmountOverlay,
} from "./overlay";

function baselinePage(): void {
  document.body.innerHTML = '<h1>Digikala product page</h1><div id="unrelated">keep-me</div>';
}

const NO_ACTIONS = { onRefresh: () => {}, onAddToWatchlist: () => {} };

function ctx(capability: OverlayContext["capability"] = "ready"): OverlayContext {
  return { capability };
}

// The gateway-generated STRING id (ObservationTarget.variantId) — DISTINCT
// from DK's own numeric nativeVariantId/nativeProductId. This is the id
// `/product?variantId=` deep links (and ProductDetail.tsx's target lookup)
// actually key on.
const VARIANT_ID = "gw-variant-abc-1";

function baseView(overrides: Partial<OverlayView> = {}): OverlayView {
  return {
    targetId: "t1",
    variantId: VARIANT_ID,
    offerCount: 0,
    sellerCount: 0,
    lowestQualifying: null,
    freshness: null,
    quality: null,
    ...overrides,
  };
}

describe("overlay — EXT-010 overlay-only DOM effect, no automated navigation/click/form input", () => {
  beforeEach(() => {
    baselinePage();
    unmountOverlay();
  });

  it("mounting appends EXACTLY ONE host element to <body>, and is idempotent", () => {
    const before = document.body.children.length;
    mountOverlay();
    expect(document.body.children.length).toBe(before + 1);
    mountOverlay(); // second mount must NOT add a second host
    expect(document.body.children.length).toBe(before + 1);
  });

  it("never mutates any node OUTSIDE its own shadow root", () => {
    const unrelated = document.getElementById("unrelated");
    const originalHtml = unrelated?.outerHTML;
    const root = mountOverlay();
    renderOverlay(root, { kind: "pending" }, NO_ACTIONS, ctx());
    expect(document.getElementById("unrelated")?.outerHTML).toBe(originalHtml);
    expect(document.title).toBe(""); // never touches document.title/head either
  });

  it("never triggers navigation — no history.pushState/location assignment is ever called by rendering", () => {
    const pushSpy = vi.spyOn(history, "pushState");
    const root = mountOverlay();
    renderOverlay(root, { kind: "unavailable" }, NO_ACTIONS, ctx());
    expect(pushSpy).not.toHaveBeenCalled();
    pushSpy.mockRestore();
  });

  it("renders an HONEST pending/unavailable state — never a fabricated value", () => {
    const root = mountOverlay();
    renderOverlay(root, { kind: "unavailable" }, NO_ACTIONS, ctx());
    const stateEl = root.querySelector('[data-role="overlay-state"]');
    expect(stateEl?.textContent).toBe("داده‌های همپوشان هنوز در دسترس نیست.");
  });

  it("renders EXT-005 fields (offers/sellers/lowest-qualifying/freshness/quality) from the given view, VERBATIM", () => {
    const view = baseView({
      offerCount: 3,
      sellerCount: 2,
      lowestQualifying: { text: "120000 IRR-rial", value: "120000", unit: "IRR-rial" },
      freshness: "fresh",
      quality: "verified",
    });
    const state: OverlayState = { kind: "ready", view, history: null };
    const root = mountOverlay();
    renderOverlay(root, state, NO_ACTIONS, ctx());
    expect(root.querySelector('[data-role="offers"]')?.textContent).toContain("3");
    expect(root.querySelector('[data-role="sellers"]')?.textContent).toContain("2");
    expect(root.querySelector('[data-role="lowest"]')?.textContent).toContain("120000 IRR-rial");
    expect(root.querySelector('[data-role="freshness"]')?.textContent).toBe("تازه");
    expect(root.querySelector('[data-role="quality"]')?.textContent).toBe("تاییدشده");
  });

  it("LOC-005: the raw lowest-qualifying price token is LTR-isolated (bidi-safe on the RTL page)", () => {
    const view = baseView({
      offerCount: 1,
      sellerCount: 1,
      lowestQualifying: { text: "120000 IRR-rial", value: "120000", unit: "IRR-rial" },
      freshness: "fresh",
      quality: "verified",
    });
    const root = mountOverlay();
    renderOverlay(root, { kind: "ready", view, history: null }, NO_ACTIONS, ctx());
    const token = root.querySelector('[data-role="lowest-value"]');
    expect(token).not.toBeNull();
    expect(token?.classList.contains("market-ops-ltr")).toBe(true);
    expect(token?.textContent).toBe("120000 IRR-rial");
  });

  it("action buttons render ONLY when capability === ready (Unknown never enables dependent UI)", () => {
    const root = mountOverlay();
    for (const capability of ["unknown", "disabled", "revoked"] as const) {
      renderOverlay(root, { kind: "pending" }, NO_ACTIONS, ctx(capability));
      expect(root.querySelector('[data-role="on-demand"]')).toBeNull();
      expect(root.querySelector('[data-role="add-watchlist"]')).toBeNull();
    }
    renderOverlay(root, { kind: "pending" }, NO_ACTIONS, ctx("ready"));
    expect(root.querySelector('[data-role="on-demand"]')).not.toBeNull();
    expect(root.querySelector('[data-role="add-watchlist"]')).not.toBeNull();
  });

  it("action buttons ONLY fire on a real user click — never auto-invoked by rendering", () => {
    const onRefresh = vi.fn();
    const onAddToWatchlist = vi.fn();
    const root = mountOverlay();
    renderOverlay(root, { kind: "pending" }, { onRefresh, onAddToWatchlist }, ctx("ready"));
    expect(onRefresh).not.toHaveBeenCalled();
    expect(onAddToWatchlist).not.toHaveBeenCalled();
    root.querySelector<HTMLButtonElement>('[data-role="on-demand"]')?.click();
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(onAddToWatchlist).not.toHaveBeenCalled();
  });

  it("EXT-008: the deep-link chip is built from the GATEWAY variantId, never DK's native ids", () => {
    const root = mountOverlay();
    renderOverlay(root, { kind: "ready", view: baseView(), history: null }, NO_ACTIONS, ctx());
    const link = root.querySelector<HTMLAnchorElement>('[data-role="deep-link-product"]');
    expect(link).not.toBeNull();
    expect(link?.getAttribute("href")).toMatch(new RegExp(`/product\\?variantId=${VARIANT_ID}$`));
    expect(link?.target).toBe("_blank");
  });

  it("EXT-008: NEVER renders a deep-link chip before the real variantId is known (pending/unavailable) — no wrong-id-space link", () => {
    const root = mountOverlay();
    renderOverlay(root, { kind: "pending" }, NO_ACTIONS, ctx());
    expect(root.querySelector('[data-role="deep-link-product"]')).toBeNull();
    renderOverlay(root, { kind: "unavailable" }, NO_ACTIONS, ctx());
    expect(root.querySelector('[data-role="deep-link-product"]')).toBeNull();
  });

  it("EXT-006: a gap-preserving history renders EVERY segment with an EXPLICIT gap marker between them — no fabricated point", () => {
    const view = baseView({ offerCount: 1, sellerCount: 1 });
    const history: HistorySeries = {
      gapCount: 1,
      segments: [
        {
          points: [
            {
              capturedAt: "2026-07-18T09:00:00Z",
              priceValue: "100000",
              priceUnit: "IRR-rial",
              quality: "verified",
            },
          ],
        },
        {
          points: [
            {
              capturedAt: "2026-07-18T14:00:00Z",
              priceValue: "105000",
              priceUnit: "IRR-rial",
              quality: "verified",
            },
          ],
        },
      ],
    };
    const root = mountOverlay();
    renderOverlay(root, { kind: "ready", view, history }, NO_ACTIONS, ctx());

    const segments = root.querySelectorAll('[data-role="history-segment"]');
    const gaps = root.querySelectorAll('[data-role="history-gap"]');
    expect(segments).toHaveLength(2);
    expect(gaps).toHaveLength(1);
    // Only the two REAL points are present — nothing fabricated in the gap.
    const points = Array.from(root.querySelectorAll('[data-role="history-point"]')).map(
      (p) => p.textContent,
    );
    expect(points).toEqual(["100000 IRR-rial", "105000 IRR-rial"]);
    // The gap marker sits BETWEEN the two segments in document order.
    const heading = root.querySelector('[data-role="history"]');
    const order = Array.from(heading?.children ?? []).map((c) => (c as HTMLElement).dataset.role);
    expect(order).toEqual(["history-title", "history-segment", "history-gap", "history-segment"]);
  });

  it("EXT-006: an unavailable history (fail-closed seam) renders an honest empty state, never a fabricated point", () => {
    const root = mountOverlay();
    renderOverlay(root, { kind: "ready", view: baseView(), history: null }, NO_ACTIONS, ctx());
    expect(root.querySelector('[data-role="history-empty"]')?.textContent).toBe("در دسترس نیست");
    expect(root.querySelectorAll('[data-role="history-point"]')).toHaveLength(0);
  });
});
