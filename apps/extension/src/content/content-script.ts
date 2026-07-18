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

let lastCapturedPath = "";
let currentProduct: ParsedProduct | null = null;

function send(msg: ExtMessage): Promise<ExtResponse> {
  return chrome.runtime.sendMessage(msg) as Promise<ExtResponse>;
}

async function captureCurrentProduct(): Promise<void> {
  const path = location.pathname;
  const productId = productIdFromPath(path);
  if (productId === null) return; // not an explicit product page — do nothing
  if (path === lastCapturedPath) return; // already captured this view
  lastCapturedPath = path;

  let raw: unknown;
  try {
    const resp = await fetch(productApiUrl(productId), {
      credentials: "include",
      headers: { accept: "application/json" },
    });
    incr("http_status", { endpoint: "product", status: resp.status });
    if (!resp.ok) {
      log("warn", "product_fetch_non_200", { status: resp.status });
      return;
    }
    raw = await resp.json();
  } catch (e) {
    log("error", "product_fetch_failed", { error: e instanceof Error ? e.message : "unknown" });
    return;
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

  currentProduct = result.product;
  const msg: ExtMessage = { kind: "capture", product: result.product };
  void send(msg);
  void refreshOverlay();
}

// refreshOverlay mounts/updates the overlay for the currently-captured product
// (EXT-005/EXT-010). Never renders a fabricated value: it shows the honest
// pending/unavailable state whenever the read seam isn't available.
async function refreshOverlay(): Promise<void> {
  const product = currentProduct;
  if (!product) return;
  const root = mountOverlay();
  renderOverlay(root, { kind: "pending" }, overlayActions(product));
  const resp = await send({ kind: "getOverlayView", product });
  const state: OverlayState = resp.ok && "overlay" in resp ? resp.overlay : { kind: "unavailable" };
  renderOverlay(root, state, overlayActions(product));
}

function overlayActions(product: ParsedProduct) {
  return {
    // A REAL user click on the overlay's refresh button — never an automated
    // trigger (EXT-010).
    onRefresh: () => {
      void send({ kind: "onDemandCapture", product }).then(() => void refreshOverlay());
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
  const fire = () => void captureCurrentProduct();
  window.addEventListener("market-ops:navigation", fire);
  window.addEventListener("popstate", fire);
  // Diagnostic-only injection (docs/09) — asks the service worker to install
  // the MAIN-world shim into THIS tab only.
  void send({ kind: "injectNavShim" });
}

window.addEventListener("beforeunload", () => {
  unmountOverlay();
});

installSpaNavigationHooks();
void captureCurrentProduct();
