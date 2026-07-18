import { productApiUrl } from "../lib/constants";
import { validateAgainstDom } from "../lib/dom-validate";
import type { ExtMessage } from "../lib/messages";
import { productIdFromPath } from "../lib/normalize";
import { incr, log } from "../lib/observability";
import { parseProductResponse } from "../lib/parse";
import { initDevErrorReporting } from "../lib/spotlight";

// Content script (docs/09). It runs ONLY on Digikala product pages, and captures
// ONLY during explicit product browsing (EXT-002, §12): it classifies the page,
// fetches the VERIFIED public product endpoint for the product the user is
// actively viewing, parses + redacts, cross-checks the DOM for drift, and hands a
// normalized, allow-listed product to the service worker. It NEVER navigates,
// clicks, submits, enumerates ids, or crawls other pages. SPA route changes are
// detected via history events + popstate.

void initDevErrorReporting("content-script");

let lastCapturedPath = "";

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

  const msg: ExtMessage = { kind: "capture", product: result.product };
  chrome.runtime.sendMessage(msg);
}

// SPA navigation: Digikala is a single-page app, so a product view can change
// without a full reload. Detect it via history pushState/replaceState + popstate,
// and re-capture the NEW product only (docs/09).
function installSpaNavigationHooks(): void {
  const fire = () => void captureCurrentProduct();
  const origPush = history.pushState.bind(history);
  const origReplace = history.replaceState.bind(history);
  history.pushState = ((...args: Parameters<typeof history.pushState>) => {
    const r = origPush(...args);
    fire();
    return r;
  }) as typeof history.pushState;
  history.replaceState = ((...args: Parameters<typeof history.replaceState>) => {
    const r = origReplace(...args);
    fire();
    return r;
  }) as typeof history.replaceState;
  window.addEventListener("popstate", fire);
}

installSpaNavigationHooks();
void captureCurrentProduct();
