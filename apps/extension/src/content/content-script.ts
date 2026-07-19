import { type Capability, captureEnabled } from "../lib/capability";
import { productApiUrl } from "../lib/constants";
import { validateAgainstDom } from "../lib/dom-validate";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import { productIdFromPath } from "../lib/normalize";
import { incr, log } from "../lib/observability";
import { parseProductResponse } from "../lib/parse";
import { initDevErrorReporting } from "../lib/spotlight";
import type { ParsedProduct } from "../lib/types";
import { mountOverlay, type OverlayState, renderOverlay, unmountOverlay } from "./overlay";

// Content script (docs/09). It runs ONLY on Digikala product pages, and captures
// ONLY during explicit product browsing (EXT-002, §12): it classifies the page,
// fetches the VERIFIED public product endpoint for the product the user is
// actively viewing, parses + redacts, cross-checks the DOM for drift, and hands a
// normalized, allow-listed product to the service worker. It NEVER navigates,
// clicks, submits, enumerates ids, or crawls other pages. SPA route changes are
// detected via the MAIN-world nav shim's CustomEvent (S31 fix — see nav-shim.ts)
// plus `popstate`. The overlay (EXT-005/EXT-010) is the ONLY additional DOM
// effect: one host element, read-only otherwise.

void initDevErrorReporting("content-script");

// SPA context state (issue #155). `navGeneration` is a monotonically-increasing
// token bumped on EVERY navigation; a capture/overlay coroutine carries the
// generation it started under and, after every await, refuses to publish onto a
// newer page. `currentProduct` is the ONLY product actions/overlay may refer to,
// and `inFlight` lets an in-progress product fetch be aborted the instant the
// user navigates — so a late response for the previous product can never
// overwrite the current one.
let navGeneration = 0;
let lastCapturedPath = "";
let currentProduct: ParsedProduct | null = null;
let inFlight: AbortController | null = null;

function send(msg: ExtMessage): Promise<ExtResponse> {
  return chrome.runtime.sendMessage(msg) as Promise<ExtResponse>;
}

function readCapability(resp: ExtResponse): Capability {
  return resp.ok && "state" in resp ? resp.state.capability : "unknown";
}

// retireContext SYNCHRONOUSLY drops all state tied to the page we are leaving
// (issue #155): it bumps the generation (invalidating every in-flight coroutine),
// clears the current product, resets the captured-path guard so returning to a
// prior pathname triggers a FRESH load, aborts any pending product fetch, and
// unmounts the overlay. Nothing tied to the old product can survive this.
function retireContext(): void {
  navGeneration += 1;
  currentProduct = null;
  lastCapturedPath = "";
  inFlight?.abort();
  inFlight = null;
  unmountOverlay();
}

// handleNavigation is the single entry point for the initial load AND every SPA
// route change: retire the prior context, then capture the (new) current product
// under the fresh generation.
function handleNavigation(): void {
  retireContext();
  void captureCurrentProduct(navGeneration);
}

async function captureCurrentProduct(generation: number): Promise<void> {
  const path = location.pathname;
  const productId = productIdFromPath(path);
  if (productId === null) return; // not an explicit product page — do nothing

  // Capability-before-fetch gate (issue #155, PRD §4.6 "Unknown never enables";
  // EXT-009 kill switch). The authenticated product endpoint is NEVER contacted
  // unless capture is READY — unknown/disabled/revoked produce ZERO
  // product-endpoint requests. `getState` is an internal runtime message, not a
  // marketplace read. The gate is observable (metric + log) so the fail-closed
  // path is provable from telemetry.
  const stateResp = await send({ kind: "getState" });
  if (generation !== navGeneration) return;
  const capability = readCapability(stateResp);
  if (!captureEnabled(capability)) {
    incr("capture_gated", { capability });
    log("info", "capture_gated", { capability });
    return;
  }

  if (path === lastCapturedPath) return; // already captured this view (double-fire)
  // Marked ONLY after the gate passes and just before the fetch: a nav-away
  // resets it to "", so returning to a prior pathname is a fresh load, not a
  // suppressed one.
  lastCapturedPath = path;

  const controller = new AbortController();
  inFlight = controller;

  let raw: unknown;
  try {
    const resp = await fetch(productApiUrl(productId), {
      credentials: "include",
      headers: { accept: "application/json" },
      signal: controller.signal,
    });
    if (generation !== navGeneration) return; // superseded mid-fetch
    incr("http_status", { endpoint: "product", status: resp.status });
    if (!resp.ok) {
      log("warn", "product_fetch_non_200", { status: resp.status });
      return;
    }
    raw = await resp.json();
    if (generation !== navGeneration) return; // superseded mid-parse
  } catch (e) {
    // An abort (navigation superseded this fetch) is expected, not an error.
    if (controller.signal.aborted || generation !== navGeneration) return;
    log("error", "product_fetch_failed", { error: e instanceof Error ? e.message : "unknown" });
    return;
  } finally {
    if (inFlight === controller) inFlight = null;
  }

  const result = parseProductResponse(raw);
  if (!result.ok) {
    // Parser-drift (§10.4): record it, never a silent guess.
    incr("parser_drift", { reason: result.reason });
    log("warn", "parser_drift", { reason: result.reason });
    return;
  }
  incr("extraction_success", { page: "product" });

  // DOM is a validation layer only; a mismatch is a recorded drift signal, never
  // a reason to block or overwrite the API evidence (docs/06/10).
  const signals = validateAgainstDom(document, result.product);
  if (signals.urlMismatch || signals.availabilityMismatch) {
    incr("selector_failure", { kind: signals.urlMismatch ? "url" : "availability" });
    log("warn", "dom_drift", {
      urlMismatch: signals.urlMismatch,
      availabilityMismatch: signals.availabilityMismatch,
    });
  }

  // Publish ONLY if the current URL and this completed response still match the
  // page we started on (issue #155) — never republish onto a newer view.
  if (generation !== navGeneration || location.pathname !== path) return;

  currentProduct = result.product;
  const msg: ExtMessage = { kind: "capture", product: result.product };
  void send(msg);
  void refreshOverlay(generation);
}

// refreshOverlay mounts/updates the overlay for the currently-captured product
// (EXT-005/EXT-010). Never renders a fabricated value: it shows the honest
// pending/unavailable state whenever the read seam isn't available. The
// action buttons are gated on the REAL current capability (read fresh via
// getState, never assumed) — Unknown/disabled/revoked renders neither.
async function refreshOverlay(generation: number): Promise<void> {
  if (generation !== navGeneration) return;
  const product = currentProduct;
  if (!product) return;
  const root = mountOverlay();
  const stateResp = await send({ kind: "getState" });
  if (generation !== navGeneration) return; // navigated away — old overlay already retired
  const capability = readCapability(stateResp);
  const context = { capability };
  renderOverlay(root, { kind: "pending" }, overlayActions(product), context);
  const resp = await send({ kind: "getOverlayView", product });
  if (generation !== navGeneration) return;
  const state: OverlayState = resp.ok && "overlay" in resp ? resp.overlay : { kind: "unavailable" };
  renderOverlay(root, state, overlayActions(product), context);
}

function overlayActions(product: ParsedProduct) {
  return {
    // A REAL user click on the overlay's refresh button — never an automated
    // trigger (EXT-010).
    onRefresh: () => {
      const generation = navGeneration;
      void send({ kind: "onDemandCapture", product }).then(() => void refreshOverlay(generation));
    },
    onAddToWatchlist: () => {
      void send({ kind: "addToWatchlist", product });
    },
  };
}

// SPA navigation (S31 fix): a Digikala route change fires from the PAGE's own
// JS in the MAIN world, so it is observed via the MAIN-world nav shim's
// CustomEvent (nav-shim.ts) rather than an isolated-world pushState patch
// (which never sees the page's own calls — see nav-shim.ts's header comment).
// `popstate` is a native browser event and is observed directly either way.
function installSpaNavigationHooks(): void {
  // Each route change retires the prior product context SYNCHRONOUSLY before any
  // new capture begins (issue #155) — see handleNavigation/retireContext.
  window.addEventListener("market-ops:navigation", handleNavigation);
  window.addEventListener("popstate", handleNavigation);
  // Diagnostic-only injection (docs/09) — asks the service worker to install
  // the MAIN-world shim into THIS tab only.
  void send({ kind: "injectNavShim" });
}

window.addEventListener("beforeunload", () => {
  unmountOverlay();
});

installSpaNavigationHooks();
// The initial load goes through the SAME retire-then-capture path (issue #155):
// it establishes generation 1 and captures the product currently being viewed.
handleNavigation();
