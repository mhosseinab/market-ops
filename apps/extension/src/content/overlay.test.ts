import { beforeEach, describe, expect, it, vi } from "vitest";
import type { OverlayView } from "../lib/overlay-data";
import { mountOverlay, type OverlayState, renderOverlay, unmountOverlay } from "./overlay";

function baselinePage(): void {
  document.body.innerHTML = '<h1>Digikala product page</h1><div id="unrelated">keep-me</div>';
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
    renderOverlay(root, { kind: "pending" }, { onRefresh: () => {}, onAddToWatchlist: () => {} });
    expect(document.getElementById("unrelated")?.outerHTML).toBe(originalHtml);
    expect(document.title).toBe(""); // never touches document.title/head either
  });

  it("never triggers navigation — no history.pushState/location assignment is ever called by rendering", () => {
    const pushSpy = vi.spyOn(history, "pushState");
    const root = mountOverlay();
    renderOverlay(
      root,
      { kind: "unavailable" },
      { onRefresh: () => {}, onAddToWatchlist: () => {} },
    );
    expect(pushSpy).not.toHaveBeenCalled();
    pushSpy.mockRestore();
  });

  it("renders an HONEST pending/unavailable state — never a fabricated value", () => {
    const root = mountOverlay();
    renderOverlay(
      root,
      { kind: "unavailable" },
      { onRefresh: () => {}, onAddToWatchlist: () => {} },
    );
    const stateEl = root.querySelector('[data-role="overlay-state"]');
    expect(stateEl?.textContent).toBe("داده‌های همپوشان هنوز در دسترس نیست.");
  });

  it("renders EXT-005 fields (offers/sellers/lowest-qualifying/freshness/quality) from the given view, VERBATIM", () => {
    const view: OverlayView = {
      targetId: "t1",
      offerCount: 3,
      sellerCount: 2,
      lowestQualifying: { text: "120000 IRR-rial", value: "120000", unit: "IRR-rial" },
      freshness: "fresh",
      quality: "verified",
    };
    const state: OverlayState = { kind: "ready", view };
    const root = mountOverlay();
    renderOverlay(root, state, { onRefresh: () => {}, onAddToWatchlist: () => {} });
    expect(root.querySelector('[data-role="offers"]')?.textContent).toContain("3");
    expect(root.querySelector('[data-role="sellers"]')?.textContent).toContain("2");
    expect(root.querySelector('[data-role="lowest"]')?.textContent).toContain("120000 IRR-rial");
    expect(root.querySelector('[data-role="freshness"]')?.textContent).toBe("تازه");
    expect(root.querySelector('[data-role="quality"]')?.textContent).toBe("تاییدشده");
  });

  it("action buttons ONLY fire on a real user click — never auto-invoked by rendering", () => {
    const onRefresh = vi.fn();
    const onAddToWatchlist = vi.fn();
    const root = mountOverlay();
    renderOverlay(root, { kind: "pending" }, { onRefresh, onAddToWatchlist });
    expect(onRefresh).not.toHaveBeenCalled();
    expect(onAddToWatchlist).not.toHaveBeenCalled();
    root.querySelector<HTMLButtonElement>('[data-role="on-demand"]')?.click();
    expect(onRefresh).toHaveBeenCalledTimes(1);
    expect(onAddToWatchlist).not.toHaveBeenCalled();
  });
});
