import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Capability } from "../lib/capability";
import { productApiUrl } from "../lib/constants";
import type { ExtMessage } from "../lib/messages";
import type { PopupState } from "../lib/storage";
import available from "../test/fixtures/product-available.json";

// content-script.ts SELF-RUNS on import (it installs SPA hooks + does the initial
// capture). So every case must set location + the chrome/fetch mocks BEFORE the
// dynamic import, then flush microtasks. These tests pin issue #155's two
// never-cut fixes: (1) capability is read BEFORE any marketplace product fetch —
// unknown/disabled/revoked produce ZERO product-endpoint requests; (2) a stale
// SPA product context (currentProduct + overlay) is retired synchronously on
// navigation and can never be republished by a late async result.

const HOST_ID = "market-ops-overlay-host";
const PRODUCT_ENDPOINT_PREFIX = "https://api.digikala.com/v2/product/";

const A_PATH = "/product/dkp-123/a/";
const B_PATH = "/product/dkp-456/b/";
const NON_PRODUCT_PATH = "/search/?q=x";

// A distinct parsed product per id so a `capture` message unambiguously names the
// product that was published (the fixture always parses to the same native id, so
// we vary product.id per path to tell A from B apart).
function fixtureWithId(id: number): unknown {
  const clone = structuredClone(available) as {
    data: { product: { id: number; url: { uri: string } } };
  };
  clone.data.product.id = id;
  clone.data.product.url.uri = `/product/dkp-${id}/x/`;
  return clone;
}

function idFromProductUrl(url: string): number {
  const m = url.match(/\/v2\/product\/(\d+)\//);
  if (!m) throw new Error(`not a product endpoint url: ${url}`);
  return Number(m[1]);
}

function okResponse(body: unknown, status = 200): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as unknown as Response;
}

function deferred<T>(): { promise: Promise<T>; resolve: (v: T) => void } {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function popupState(capability: Capability): PopupState {
  return {
    capability,
    marketplaceAccountId: null,
    lastUploadAt: null,
    queuedCount: 0,
    degradation: null,
    scheduleEnabled: false,
    deadLetter: [],
  };
}

interface Harness {
  fetchMock: ReturnType<typeof vi.fn>;
  messages: ExtMessage[];
  capabilityRef: { value: Capability };
}

let addedListeners: Array<[string, EventListenerOrEventListenerObject]>;
let restoreAddEventListener: () => void;

// The content script registers window listeners (market-ops:navigation, popstate,
// beforeunload) at import. Across vi.resetModules() imports these would otherwise
// accumulate: a PRIOR module's stale listeners would still fire on our dispatch
// and pollute the current fetch/message counts. We record every registration and
// remove them in afterEach so exactly ONE module instance is ever live.
function trackListeners(): void {
  addedListeners = [];
  const orig = window.addEventListener.bind(window);
  const spy = vi.spyOn(window, "addEventListener").mockImplementation(((
    type: string,
    fn: EventListenerOrEventListenerObject,
    opts?: unknown,
  ) => {
    addedListeners.push([type, fn]);
    orig(type, fn, opts as AddEventListenerOptions);
  }) as typeof window.addEventListener);
  restoreAddEventListener = () => spy.mockRestore();
}

function installMocks(capability: Capability): Harness {
  const messages: ExtMessage[] = [];
  const capabilityRef = { value: capability };
  const fetchMock = vi.fn(async (url: string) => okResponse(fixtureWithId(idFromProductUrl(url))));
  const sendMessage = vi.fn(async (msg: ExtMessage) => {
    messages.push(msg);
    if (msg.kind === "getState") return { ok: true, state: popupState(capabilityRef.value) };
    if (msg.kind === "getOverlayView") return { ok: true, overlay: { kind: "unavailable" } };
    return { ok: true };
  });
  (globalThis as unknown as { chrome: unknown }).chrome = {
    runtime: { sendMessage },
  };
  (globalThis as unknown as { fetch: unknown }).fetch = fetchMock;
  return { fetchMock, messages, capabilityRef };
}

function navigateTo(path: string): void {
  window.history.pushState({}, "", path);
  window.dispatchEvent(new CustomEvent("market-ops:navigation"));
}

const tick = () => new Promise((r) => setTimeout(r, 0));
async function flush(): Promise<void> {
  for (let i = 0; i < 5; i++) await tick();
}

function productFetchCalls(fetchMock: ReturnType<typeof vi.fn>): string[] {
  return fetchMock.mock.calls
    .map((c) => c[0] as string)
    .filter((u) => typeof u === "string" && u.startsWith(PRODUCT_ENDPOINT_PREFIX));
}

function captureMessages(messages: ExtMessage[]): Array<Extract<ExtMessage, { kind: "capture" }>> {
  return messages.filter(
    (m): m is Extract<ExtMessage, { kind: "capture" }> => m.kind === "capture",
  );
}

async function loadContentScript(path: string, capability: Capability): Promise<Harness> {
  window.history.pushState({}, "", path);
  const h = installMocks(capability);
  trackListeners();
  vi.resetModules();
  await import("./content-script");
  await flush();
  return h;
}

beforeEach(() => {
  document.body.innerHTML = "";
  window.history.pushState({}, "", "/");
});

afterEach(() => {
  for (const [type, fn] of addedListeners) window.removeEventListener(type, fn);
  restoreAddEventListener?.();
  vi.restoreAllMocks();
});

describe("content-script #155 — capability is read BEFORE any marketplace product fetch (never-cut)", () => {
  for (const capability of ["unknown", "disabled", "revoked"] as const) {
    it(`capability=${capability}: ZERO product-endpoint fetches on a product page (fail closed)`, async () => {
      const { fetchMock } = await loadContentScript(A_PATH, capability);
      expect(productFetchCalls(fetchMock)).toHaveLength(0);
    });
  }
});

describe("content-script #155 — stale SPA product context is retired on navigation (never-cut)", () => {
  it("product -> non-product navigation SYNCHRONOUSLY removes the overlay host and clears context", async () => {
    const { messages } = await loadContentScript(A_PATH, "ready");
    // A ready product mounts the overlay.
    expect(document.getElementById(HOST_ID)).not.toBeNull();
    expect(captureMessages(messages)).toHaveLength(1);

    // Navigating away is a SYNCHRONOUS retirement — assert immediately, no flush.
    window.history.pushState({}, "", NON_PRODUCT_PATH);
    window.dispatchEvent(new CustomEvent("market-ops:navigation"));
    expect(document.getElementById(HOST_ID)).toBeNull();

    await flush();
    // A non-product page never fetches or captures again.
    expect(captureMessages(messages)).toHaveLength(1);
  });

  it("product A (valid) -> product B (failing fetch): no A overlay/context survives on B", async () => {
    const { fetchMock, messages } = await loadContentScript(A_PATH, "ready");
    expect(document.getElementById(HOST_ID)).not.toBeNull();

    // B's fetch fails (non-200) — A must NOT remain published on B.
    fetchMock.mockImplementation(async (url: string) => {
      if (idFromProductUrl(url) === 456) return okResponse(null, 502);
      return okResponse(fixtureWithId(idFromProductUrl(url)));
    });
    navigateTo(B_PATH);
    // Retirement is synchronous: A's overlay is gone the instant we navigate.
    expect(document.getElementById(HOST_ID)).toBeNull();

    await flush();
    // B failed to parse/publish, so no overlay re-mounts and no B capture exists;
    // crucially, A's capture is not re-sent.
    expect(document.getElementById(HOST_ID)).toBeNull();
    const captures = captureMessages(messages);
    expect(captures.map((c) => c.product.nativeProductId)).toEqual([123]);
  });

  it("a LATE-resolving A fetch can NEVER overwrite an already-current B (generation token)", async () => {
    // A's fetch is held open; we navigate to B (which completes) and only THEN
    // release A's response. The generation guard must drop A's late result.
    const aDef = deferred<Response>();
    window.history.pushState({}, "", A_PATH);
    const h = installMocks("ready");
    h.fetchMock.mockImplementation(async (url: string) => {
      const id = idFromProductUrl(url);
      if (id === 123) return aDef.promise;
      return okResponse(fixtureWithId(id));
    });
    trackListeners();
    vi.resetModules();
    await import("./content-script");
    await flush(); // A's fetch is now pending (awaiting aDef)

    expect(captureMessages(h.messages)).toHaveLength(0);

    navigateTo(B_PATH);
    await flush(); // B completes and publishes

    // Now release A's stale response.
    aDef.resolve(okResponse(fixtureWithId(123)));
    await flush();

    const ids = captureMessages(h.messages).map((c) => c.product.nativeProductId);
    // B (456) is published; the late A (123) is dropped by the generation guard.
    expect(ids).toContain(456);
    expect(ids).not.toContain(123);
  });

  it("returning to a prior pathname performs a FRESH bounded load (A -> B -> A refetches A)", async () => {
    const { fetchMock } = await loadContentScript(A_PATH, "ready");
    expect(productFetchCalls(fetchMock).filter((u) => idFromProductUrl(u) === 123)).toHaveLength(1);

    navigateTo(B_PATH);
    await flush();

    navigateTo(A_PATH);
    await flush();

    // lastCapturedPath was reset on the nav-away, so returning re-fetches A —
    // never suppressed as an already-captured view.
    expect(productFetchCalls(fetchMock).filter((u) => idFromProductUrl(u) === 123)).toHaveLength(2);
  });
});

describe("content-script #155 — positive path (capability=ready on a product page)", () => {
  it("performs EXACTLY ONE product fetch, sends a capture message, and mounts the overlay", async () => {
    const { fetchMock, messages } = await loadContentScript(A_PATH, "ready");

    const calls = productFetchCalls(fetchMock);
    expect(calls).toHaveLength(1);
    expect(calls[0]).toBe(productApiUrl(123));

    const captures = captureMessages(messages);
    expect(captures).toHaveLength(1);
    expect(captures[0]?.product.nativeProductId).toBe(123);

    expect(document.getElementById(HOST_ID)).not.toBeNull();
  });
});
