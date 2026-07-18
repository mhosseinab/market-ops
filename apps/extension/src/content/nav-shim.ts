// MAIN-world navigation shim (S31 carry-forward fix; docs/09: "Page-context
// interception is diagnostic-only, capability-gated, and never modifies page
// traffic"). Digikala's SPA router calls `history.pushState`/`replaceState`
// from the PAGE's own JS context (MAIN world). A content script's ISOLATED
// world gets its own copy of `window`/`history`, so monkey-patching
// `history.pushState` there (the S30 approach) never observes the page's own
// calls — that was the S30 carry-forward bug ("content-script SPA nav
// detection largely inert").
//
// This file is injected via `chrome.scripting.executeScript({world:"MAIN"})`
// (the `scripting` permission, docs/09) ONLY into the active Digikala product
// tab that already matched the content script's host permissions — never any
// other origin. It is STRICTLY diagnostic: it calls the ORIGINAL
// pushState/replaceState UNCHANGED (never alters arguments, never blocks the
// call, never redirects) and only ADDITIONALLY dispatches a CustomEvent on
// `window` so the isolated-world content script (which shares the DOM/event
// bus, not the JS heap) can react. It never mutates page traffic.
//
// Self-contained: no imports (declarative-content-script build constraint —
// same as content-script.ts's IIFE build).
(() => {
  const EVENT_NAME = "market-ops:navigation";
  const w = window as unknown as {
    history: History;
    __marketOpsNavShimInstalled__?: boolean;
  };
  if (w.__marketOpsNavShimInstalled__) return; // idempotent — never double-patch
  w.__marketOpsNavShimInstalled__ = true;

  const notify = () => window.dispatchEvent(new CustomEvent(EVENT_NAME));

  const originalPush = history.pushState.bind(history);
  history.pushState = ((...args: Parameters<typeof history.pushState>) => {
    const result = originalPush(...args);
    notify();
    return result;
  }) as typeof history.pushState;

  const originalReplace = history.replaceState.bind(history);
  history.replaceState = ((...args: Parameters<typeof history.replaceState>) => {
    const result = originalReplace(...args);
    notify();
    return result;
  }) as typeof history.replaceState;
})();

// A top-level `export {}` makes this file a MODULE for TypeScript/isolatedModules
// (it has no other import/export) without adding any runtime import — the IIFE
// build still emits a single self-contained, import-less script.
export {};
