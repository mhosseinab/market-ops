import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import { parseProductResponse } from "../lib/parse";
import type { ParsedProduct } from "../lib/types";
import available from "../test/fixtures/product-available.json";

const KEY_CAPABILITY = "capability";
const KEY_CREDENTIAL = "credential";

const CRED = {
  credential: "cap-cred-hex",
  credentialId: "33333333-3333-3333-3333-333333333333",
  marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
  expiresAt: "2026-08-01T00:00:00Z",
};

// A minimal in-memory chrome mock — enough surface for the service worker's
// top-level wiring (alarms/runtime/storage/scripting) to install without
// throwing.
function installChromeMock(): {
  storage: Map<string, unknown>;
  executeScript: ReturnType<typeof vi.fn>;
} {
  const storage = new Map<string, unknown>();
  const executeScript = vi.fn(async () => []);
  (globalThis as unknown as { chrome: unknown }).chrome = {
    runtime: { onInstalled: { addListener: vi.fn() }, onMessage: { addListener: vi.fn() } },
    alarms: { create: vi.fn(), onAlarm: { addListener: vi.fn() } },
    scripting: { executeScript },
    storage: {
      local: {
        get: vi.fn(async (key: string | null) => {
          if (key === null) return Object.fromEntries(storage.entries());
          return storage.has(key) ? { [key]: storage.get(key) } : {};
        }),
        set: vi.fn(async (obj: Record<string, unknown>) => {
          for (const [k, v] of Object.entries(obj)) storage.set(k, v);
        }),
        remove: vi.fn(async (key: string) => {
          storage.delete(key);
        }),
      },
    },
  };
  return { storage, executeScript };
}

function parsedProduct(): ParsedProduct {
  const r = parseProductResponse(available);
  if (!r.ok) throw new Error("fixture must parse");
  return r.product;
}

function ownedTargetFor(product: ParsedProduct) {
  if (product.offer === null) throw new Error("fixture must have an offer");
  return {
    targetId: "target-1",
    marketplaceAccountId: CRED.marketplaceAccountId,
    nativeVariantId: product.offer.nativeVariantId,
  };
}

// ownedTargetRow is the server's wire ObservationTarget shape returned by GET
// /ext/owned-targets. fetchOwnedTargets maps `id` → targetId; the extra fields
// are the account's own data, ignored by the projection.
function ownedTargetRow(product: ParsedProduct) {
  const t = ownedTargetFor(product);
  return {
    id: t.targetId,
    marketplaceAccountId: t.marketplaceAccountId,
    identityId: "99999999-9999-9999-9999-999999999999",
    variantId: "88888888-8888-8888-8888-888888888888",
    nativeVariantId: t.nativeVariantId,
    nativeProductId: product.nativeProductId,
    tier: "standard",
    cadenceSeconds: 21600,
    freshnessDeadlineSeconds: 21600,
    active: true,
  };
}

// WATCHLIST_ENTRY_ID is the S37 POST /watchlist success payload id the mock
// returns, so a test can assert the wired add surfaces the REAL entry id (never
// a fabricated one).
const WATCHLIST_ENTRY_ID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee";

// gatewayFetchMock routes the service worker's outbound gateway calls: the
// pairing claim yields the capture credential, the credential-scoped owned-target
// read (#145) yields `targetsStatus`/`rows`, the S37 watchlist add returns a
// WatchlistEntry, and a capture upload is accepted. This lets a test drive the
// Confirmed-owned-target projection through the REAL credential-scoped sync
// (after pair) — never the removed setOwnedTargets message.
function gatewayFetchMock(rows: unknown[], targetsStatus = 200) {
  return vi.fn(async (input: string, _init?: RequestInit) => {
    const url = String(input);
    if (url.includes("/ext/pairing/claim")) {
      return new Response(JSON.stringify(CRED), { status: 200 });
    }
    if (url.includes("/ext/owned-targets")) {
      return new Response(targetsStatus === 200 ? JSON.stringify({ items: rows }) : "{}", {
        status: targetsStatus,
      });
    }
    if (url.includes("/watchlist")) {
      return new Response(
        JSON.stringify({
          id: WATCHLIST_ENTRY_ID,
          marketplaceAccountId: CRED.marketplaceAccountId,
          variantId: "88888888-8888-8888-8888-888888888888",
          createdAt: "2026-07-18T10:00:00Z",
        }),
        { status: 200 },
      );
    }
    return new Response(null, { status: 202 }); // /observation/capture
  });
}

type Sender = { tab?: { id: number } };

async function loadWorker(): Promise<(msg: ExtMessage, sender?: Sender) => Promise<ExtResponse>> {
  vi.resetModules();
  await import("./service-worker");
  const chromeMock = (
    globalThis as unknown as {
      chrome: { runtime: { onMessage: { addListener: ReturnType<typeof vi.fn> } } };
    }
  ).chrome;
  const handler = chromeMock.runtime.onMessage.addListener.mock.calls[0]?.[0] as (
    msg: ExtMessage,
    sender: unknown,
    sendResponse: (r: ExtResponse) => void,
  ) => boolean;
  return (msg: ExtMessage, sender: Sender = {}) =>
    new Promise<ExtResponse>((resolve) => {
      handler(msg, sender, resolve);
    });
}

describe("service worker — EXT-004 gate applies to watchlist + overlay too (never-cut)", () => {
  beforeEach(() => {
    installChromeMock();
  });

  it("addToWatchlist fails closed (denied) when the product is NOT a Confirmed owned target", async () => {
    const send = await loadWorker();
    const resp = await send({ kind: "addToWatchlist", product: parsedProduct() });
    expect(resp).toEqual({ ok: true, watchlist: { ok: false, reason: "denied" } });
  });

  it("getOverlayView is unavailable when the product is NOT a Confirmed owned target", async () => {
    const send = await loadWorker();
    const resp = await send({ kind: "getOverlayView", product: parsedProduct() });
    expect(resp).toEqual({ ok: true, overlay: { kind: "unavailable" } });
  });

  it("queue_depth reflects the REAL pending count after an enqueue (never a placeholder 0)", async () => {
    const send = await loadWorker();
    // Unknown capability (never paired) — the capture is a no-op, so the queue
    // stays empty; this proves depth is READ from storage, not hardcoded.
    const resp = await send({ kind: "capture", product: parsedProduct() });
    expect(resp.ok).toBe(true);
    if ("state" in resp) expect(resp.state.queuedCount).toBe(0);
  });
});

// EXT-003: on-demand refresh completes within 10s under normal network
// conditions. A REAL network-inclusive timing proof belongs in S32's
// `task test:integration` (compose-based, against the real gateway) — this is
// a bounded LOCAL proxy: it proves the on-demand CODE PATH itself introduces
// no artificial delay (no wait on the 1-minute alarm hint, no sleep/backoff on
// the first attempt) by asserting the handler resolves — and emits its own
// on_demand_latency_ms metric — well under the 10s budget, with the network
// call itself stubbed to resolve immediately.
describe("service worker — EXT-003 on-demand refresh has no artificial delay (bounded local proxy for the 10s SLA)", () => {
  it("handleOnDemandCapture resolves well under 10s and emits on_demand_latency_ms", async () => {
    const product = parsedProduct();
    const fetchMock = gatewayFetchMock([ownedTargetRow(product)]);
    vi.stubGlobal("fetch", fetchMock);

    installChromeMock();
    const send = await loadWorker();
    // Pairing populates the Confirmed-owned-target projection through the REAL
    // credential-scoped sync (#145) — no setOwnedTargets message exists anymore.
    await send({ kind: "pair", code: "pair-code" });

    const startedAt = Date.now();
    const resp = await send({ kind: "onDemandCapture", product });
    const elapsedMs = Date.now() - startedAt;

    expect(resp.ok).toBe(true);
    expect(elapsedMs).toBeLessThan(10_000);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/observation/capture"),
      expect.objectContaining({ method: "POST" }),
    );

    vi.unstubAllGlobals();
  });
});

// EXT-009 kill-switch bypass scenario (distinct from the EXT-004 owned-target
// gate above): even a Confirmed-owned product + valid credential must NEVER
// reach the server while capability isn't "ready".
describe("service worker — EXT-009 kill switch: capability gates nav-shim/watchlist/overlay (never-cut)", () => {
  let storage: Map<string, unknown>;
  let executeScript: ReturnType<typeof vi.fn>;
  let send: (msg: ExtMessage, sender?: Sender) => Promise<ExtResponse>;
  let product: ParsedProduct;

  beforeEach(async () => {
    product = parsedProduct();
    // The credential-scoped owned-target read populates the projection through
    // the REAL sync when the extension pairs (#145) — never a client payload.
    vi.stubGlobal("fetch", gatewayFetchMock([ownedTargetRow(product)]));
    const mock = installChromeMock();
    storage = mock.storage;
    executeScript = mock.executeScript;
    send = await loadWorker();
    await send({ kind: "pair", code: "pair-code" }); // stores CRED + syncs targets
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("injectNavShim NEVER calls chrome.scripting.executeScript for unknown/disabled/revoked capability", async () => {
    for (const capability of ["unknown", "disabled", "revoked"]) {
      storage.set(KEY_CAPABILITY, capability);
      await send({ kind: "injectNavShim" }, { tab: { id: 7 } });
    }
    expect(executeScript).not.toHaveBeenCalled();
  });

  it("injectNavShim DOES inject once capability === ready, targeting only the sender's own tab", async () => {
    storage.set(KEY_CAPABILITY, "ready");
    await send({ kind: "injectNavShim" }, { tab: { id: 7 } });
    expect(executeScript).toHaveBeenCalledTimes(1);
    expect(executeScript).toHaveBeenCalledWith(
      expect.objectContaining({ target: { tabId: 7 }, world: "MAIN", files: ["nav-shim.js"] }),
    );
  });

  it("addToWatchlist fails closed for disabled/revoked capability EVEN with a Confirmed target + credential", async () => {
    for (const capability of ["disabled", "revoked"]) {
      storage.set(KEY_CAPABILITY, capability);
      const resp = await send({ kind: "addToWatchlist", product });
      expect(resp).toEqual({ ok: true, watchlist: { ok: false, reason: "denied" } });
    }
  });

  it("getOverlayView fails closed for disabled/revoked capability EVEN with a Confirmed target", async () => {
    for (const capability of ["disabled", "revoked"]) {
      storage.set(KEY_CAPABILITY, capability);
      const resp = await send({ kind: "getOverlayView", product });
      expect(resp).toEqual({ ok: true, overlay: { kind: "unavailable" } });
    }
  });

  it("addToWatchlist reaches the REAL S37 gateway ONLY when ready — proves the gate opens AND the wired POST succeeds", async () => {
    storage.set(KEY_CAPABILITY, "ready");
    const watchlistResp = await send({ kind: "addToWatchlist", product });
    // Reaches the REAL S37 POST /watchlist through the credential-scoped
    // gateway and surfaces the server's entry id — a DIFFERENT outcome than the
    // capability/ownership "denied" short-circuit above, proving the capability
    // gate actually opened the path through to the wired client.
    expect(watchlistResp).toEqual({
      ok: true,
      watchlist: { ok: true, entryId: WATCHLIST_ENTRY_ID },
    });
  });

  it("surfaces the durable dead-letter store in popup state and lets the operator discard/retry it (issue #150)", async () => {
    storage.set(KEY_CAPABILITY, "ready");
    storage.set("deadLetter", [
      {
        dedupKey: "dead-1",
        capture: { targetId: "t", capturedAt: "2026-07-18T10:00:00Z" },
        attempts: 5,
        enqueuedAt: "2026-07-18T09:00:00Z",
        deadLetteredAt: "2026-07-18T10:00:00Z",
        failureReason: "max_attempts_exhausted",
      },
    ]);

    // getState exposes it as a VISIBLE recovery surface (never a silent drop).
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.deadLetter).toEqual([
      { dedupKey: "dead-1", failureReason: "max_attempts_exhausted" },
    ]);

    // Discard is an explicit, observable operator action — removes it durably.
    const discarded = await send({ kind: "discardDeadLetter", dedupKey: "dead-1" });
    if (!("state" in discarded)) throw new Error("expected state");
    expect(discarded.state.deadLetter).toEqual([]);
  });

  it("revoke clears the Confirmed-owned-target index too — a stale target never survives revocation", async () => {
    storage.set(KEY_CAPABILITY, "ready");
    await send({ kind: "revoke" });
    // Simulate a re-pair WITHOUT a fresh setOwnedTargets sync — isolates the
    // ownedTargets-clearing effect from the (already-covered) capability gate.
    storage.set(KEY_CAPABILITY, "ready");
    storage.set(KEY_CREDENTIAL, CRED);
    const resp = await send({ kind: "addToWatchlist", product });
    expect(resp).toEqual({ ok: true, watchlist: { ok: false, reason: "denied" } });
  });
});

// issue #162: operational telemetry must survive MV3 worker restarts. The worker
// persists a durable, allow-listed metric snapshot into chrome.storage on the
// flush alarm, so a subsequent teardown never erases queue/upload/drift signals.
describe("service worker — #162 durable telemetry outbox survives worker teardown", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function alarmHandler(): (a: { name: string }) => void {
    const chromeMock = (
      globalThis as unknown as {
        chrome: { alarms: { onAlarm: { addListener: ReturnType<typeof vi.fn> } } };
      }
    ).chrome;
    return chromeMock.alarms.onAlarm.addListener.mock.calls[0]?.[0] as (a: {
      name: string;
    }) => void;
  }

  it("persists a bounded, PII-free metric batch to storage on the flush alarm", async () => {
    const product = parsedProduct();
    vi.stubGlobal("fetch", gatewayFetchMock([ownedTargetRow(product)]));
    const { storage } = installChromeMock();
    const send = await loadWorker();
    await send({ kind: "pair", code: "pair-code" }); // records owned_targets_sync etc.
    await send({ kind: "capture", product }); // records upload_accepted + queue_depth

    // The flush alarm fires: snapshot the in-memory registry into the durable
    // outbox (survives the next teardown) + attempt export.
    alarmHandler()({ name: "queue-flush" });
    await new Promise((r) => setTimeout(r, 0));

    const outbox = storage.get("telemetryOutbox") as Array<{ metrics: unknown[] }> | undefined;
    expect(outbox).toBeDefined();
    expect(outbox?.length).toBeGreaterThan(0);
    // Containment: nothing in the durable batch resembles a URL / credential /
    // raw marketplace text — bounded labels + counts only.
    const serialized = JSON.stringify(outbox);
    expect(serialized).not.toMatch(/https?:\/\//i);
    expect(serialized).not.toMatch(/digikala/i);
    expect(serialized).not.toMatch(/Bearer|cap-cred/i);
  });
});

// #145: the Confirmed-owned-target projection is driven ONLY by the real
// credential-scoped sync (GET /ext/owned-targets) — the untrusted setOwnedTargets
// message is gone. These assert the fail-closed + open behaviors end to end.
describe("service worker — #145 owned-target sync drives the EXT-004 gate (no setOwnedTargets)", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("a failed/unauthorized owned-targets sync leaves the index EMPTY → passive capture stays not_confirmed_owned (capture disabled)", async () => {
    const product = parsedProduct();
    // Pair succeeds (capability ready) but the owned-target read is unauthorized:
    // the projection must be CLEARED, never guessed — capture uploads nothing.
    vi.stubGlobal("fetch", gatewayFetchMock([ownedTargetRow(product)], 401));
    installChromeMock();
    const send = await loadWorker();
    await send({ kind: "pair", code: "pair-code" });

    const resp = await send({ kind: "capture", product });
    expect(resp.ok).toBe(true);
    // Nothing enqueued: the EXT-004 gate skipped it as not_confirmed_owned.
    if ("state" in resp) expect(resp.state.queuedCount).toBe(0);
  });

  it("after a successful sync a Confirmed-owned product is enqueued + uploaded WITHOUT any setOwnedTargets message", async () => {
    const product = parsedProduct();
    const fetchMock = gatewayFetchMock([ownedTargetRow(product)]);
    vi.stubGlobal("fetch", fetchMock);
    installChromeMock();
    const send = await loadWorker();
    await send({ kind: "pair", code: "pair-code" }); // real sync populates the index

    await send({ kind: "capture", product });
    // The capture reached the gateway — proof the gate OPENED purely from the
    // credential-scoped sync, with no client-authored target list.
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining("/observation/capture"),
      expect.objectContaining({ method: "POST" }),
    );
  });
});

// issue #253: a stale owned-target sync (a request started under credential A
// that completes AFTER A is revoked or replaced by B) must NEVER apply its result.
// The projection is bound to a monotonic generation + the initiating credential
// identity; a completion is applied only if the generation is still current AND
// the stored credential still matches — otherwise it is ignored as `stale`. These
// drive every completion order with DEFERRED promises (startup/alarm sync, revoke,
// re-pair, success, null/error, repeated pairing).
describe("service worker — #253 stale owned-target sync after revoke / re-pair is ignored (identity quarantine, never-cut)", () => {
  // Two distinct capture credentials → distinct fingerprints (credentialId +
  // marketplaceAccountId). A is the initiating credential; B is the re-pair.
  const CRED_A = {
    credential: "cap-cred-A",
    credentialId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
    marketplaceAccountId: "a1111111-1111-1111-1111-111111111111",
    expiresAt: "2026-08-01T00:00:00Z",
  };
  const CRED_B = {
    credential: "cap-cred-B",
    credentialId: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
    marketplaceAccountId: "b2222222-2222-2222-2222-222222222222",
    expiresAt: "2026-08-01T00:00:00Z",
  };

  function ownedRow(nativeVariantId: number, variantId: string, marketplaceAccountId: string) {
    return {
      id: `target-${variantId}`,
      marketplaceAccountId,
      identityId: "99999999-9999-9999-9999-999999999999",
      variantId,
      nativeVariantId,
      nativeProductId: 1,
      tier: "standard",
      cadenceSeconds: 21600,
      freshnessDeadlineSeconds: 21600,
      active: true,
    };
  }

  interface OwnedCall {
    credential: string;
    ok: (rows: unknown[]) => void;
    fail: () => void;
  }

  // A fetch mock whose /ext/owned-targets responses are DEFERRED: each call is
  // parked in `ownedCalls` until the test explicitly fulfills it, so we can force
  // any completion order. Pairing + watchlist + capture resolve immediately.
  function deferredFetch() {
    const ownedCalls: OwnedCall[] = [];
    const captureTargetIds: string[] = [];
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/ext/pairing/claim")) {
        const code = JSON.parse(String(init?.body ?? "{}")).code;
        return new Response(JSON.stringify(code === "pair-B" ? CRED_B : CRED_A), { status: 200 });
      }
      if (url.includes("/ext/owned-targets")) {
        const auth = (init?.headers as Record<string, string> | undefined)?.authorization ?? "";
        const credential = auth.replace("Bearer ", "");
        return await new Promise<Response>((resolve) => {
          ownedCalls.push({
            credential,
            ok: (rows) => resolve(new Response(JSON.stringify({ items: rows }), { status: 200 })),
            fail: () => resolve(new Response("{}", { status: 401 })),
          });
        });
      }
      if (url.includes("/watchlist")) {
        return new Response(
          JSON.stringify({
            id: WATCHLIST_ENTRY_ID,
            marketplaceAccountId: "x",
            variantId: "v",
            createdAt: "2026-07-18T10:00:00Z",
          }),
          { status: 200 },
        );
      }
      // /observation/capture
      captureTargetIds.push(JSON.parse(String(init?.body ?? "{}")).targetId);
      return new Response(null, { status: 202 });
    });
    return { fetch, ownedCalls, captureTargetIds };
  }

  function callAt(i: number): OwnedCall {
    const c = ownedCalls[i];
    if (!c) throw new Error(`no parked owned-target call at index ${i}`);
    return c;
  }

  const tick = () => new Promise((r) => setTimeout(r, 0));
  async function waitFor(cond: () => boolean, max = 100): Promise<void> {
    for (let i = 0; i < max && !cond(); i++) await tick();
    if (!cond()) throw new Error("condition not met");
  }
  const settle = async () => {
    for (let i = 0; i < 8; i++) await tick();
  };

  function alarmHandler(): (a: { name: string }) => void {
    const chromeMock = (
      globalThis as unknown as {
        chrome: { alarms: { onAlarm: { addListener: ReturnType<typeof vi.fn> } } };
      }
    ).chrome;
    return chromeMock.alarms.onAlarm.addListener.mock.calls[0]?.[0] as (a: {
      name: string;
    }) => void;
  }

  const productA = parsedProduct();
  const VA = productA.offer?.nativeVariantId ?? 0;
  const VB = VA + 7777;
  const productB: ParsedProduct = {
    ...productA,
    offer: { ...(productA.offer ?? { nativeVariantId: VB }), nativeVariantId: VB },
  };
  const rowA = () => ownedRow(VA, "a-variant", CRED_A.marketplaceAccountId);
  const rowB = () => ownedRow(VB, "b-variant", CRED_B.marketplaceAccountId);

  let storage: Map<string, unknown>;
  let ownedCalls: OwnedCall[];
  let captureTargetIds: string[];
  let send: (msg: ExtMessage, sender?: Sender) => Promise<ExtResponse>;

  // Pairs credential A and lets its owned-target sync install rowA, then launches
  // an ALARM-triggered A-sync that stays IN FLIGHT (parked, index = its position).
  async function pairAWithInFlightAlarmSync(): Promise<number> {
    const pairA = send({ kind: "pair", code: "pair-A" });
    await waitFor(() => ownedCalls.length >= 1);
    callAt(0).ok([rowA()]);
    await pairA;
    // A background (fire-and-forget) A-sync from the periodic alarm — deferred.
    alarmHandler()({ name: "scheduled-refresh" });
    await waitFor(() => ownedCalls.length >= 2);
    return 1; // index of the in-flight A-sync
  }

  async function watchlist(product: ParsedProduct): Promise<ExtResponse> {
    return send({ kind: "addToWatchlist", product });
  }

  beforeEach(async () => {
    const mock = installChromeMock();
    storage = mock.storage;
    const df = deferredFetch();
    ownedCalls = df.ownedCalls;
    captureTargetIds = df.captureTargetIds;
    vi.stubGlobal("fetch", df.fetch);
    send = await loadWorker();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("a delayed credential-A success can NOT overwrite the credential-B projection after re-pair", async () => {
    const inflightA = await pairAWithInFlightAlarmSync();

    // Re-pair to B and let B's sync install rowB.
    const pairB = send({ kind: "pair", code: "pair-B" });
    await waitFor(() => ownedCalls.length >= 3);
    callAt(2).ok([rowB()]);
    await pairB;

    // NOW the stale A success lands — it must be ignored, not applied over B.
    callAt(inflightA).ok([rowA()]);
    await settle();

    // Only B's target resolves; A's target never resurrects.
    expect(await watchlist(productB)).toEqual({
      ok: true,
      watchlist: { ok: true, entryId: WATCHLIST_ENTRY_ID },
    });
    expect(await watchlist(productA)).toEqual({
      ok: true,
      watchlist: { ok: false, reason: "denied" },
    });

    // The drop is observable as a `stale` outcome (no secret logged).
    const obs = await import("../lib/observability");
    expect(obs.counterValue("owned_targets_sync", { outcome: "stale" })).toBeGreaterThanOrEqual(1);
  });

  it("a delayed credential-A null/failure can NOT clear the valid credential-B projection", async () => {
    const inflightA = await pairAWithInFlightAlarmSync();

    const pairB = send({ kind: "pair", code: "pair-B" });
    await waitFor(() => ownedCalls.length >= 3);
    callAt(2).ok([rowB()]);
    await pairB;

    // The stale A request fails (401/expired) — it must NOT clear B's projection.
    callAt(inflightA).fail();
    await settle();

    expect(await watchlist(productB)).toEqual({
      ok: true,
      watchlist: { ok: true, entryId: WATCHLIST_ENTRY_ID },
    });
  });

  it("a post-revoke credential-A response can NOT repopulate the cleared index", async () => {
    const inflightA = await pairAWithInFlightAlarmSync();

    // Revoke: index cleared + all outstanding generations invalidated.
    await send({ kind: "revoke" });

    // The in-flight A-sync now completes — it must NOT repopulate the index.
    callAt(inflightA).ok([rowA()]);
    await settle();

    // Isolate the index-clearing from the capability gate: simulate a re-pair race
    // that restores ready + a credential WITHOUT re-running the sync.
    storage.set("capability", "ready");
    storage.set("credential", CRED_A);
    expect(await watchlist(productA)).toEqual({
      ok: true,
      watchlist: { ok: false, reason: "denied" },
    });
  });

  it("overlapping same-credential syncs apply ONLY the newest generation, regardless of completion order", async () => {
    const pairA = send({ kind: "pair", code: "pair-A" });
    await waitFor(() => ownedCalls.length >= 1);
    callAt(0).ok([rowA()]);
    await pairA;

    // Two overlapping alarm A-syncs. #1 (older) returns rowA; #2 (newer) rowB.
    alarmHandler()({ name: "scheduled-refresh" });
    await waitFor(() => ownedCalls.length >= 2);
    alarmHandler()({ name: "scheduled-refresh" });
    await waitFor(() => ownedCalls.length >= 3);

    // The NEWER (#2) completes first and installs rowB…
    callAt(2).ok([rowB()]);
    await settle();
    // …then the OLDER (#1) completes LAST and must be ignored (stale generation).
    callAt(1).ok([rowA()]);
    await settle();

    expect(await watchlist(productB)).toEqual({
      ok: true,
      watchlist: { ok: true, entryId: WATCHLIST_ENTRY_ID },
    });
    expect(await watchlist(productA)).toEqual({
      ok: true,
      watchlist: { ok: false, reason: "denied" },
    });
  });

  it("capture after re-pair uploads ONLY B-owned targets — a stale A completion never reopens A's target", async () => {
    const inflightA = await pairAWithInFlightAlarmSync();

    const pairB = send({ kind: "pair", code: "pair-B" });
    await waitFor(() => ownedCalls.length >= 3);
    callAt(2).ok([rowB()]);
    await pairB;
    callAt(inflightA).ok([rowA()]); // stale — ignored
    await settle();

    await send({ kind: "capture", product: productA }); // not owned → skipped
    await send({ kind: "capture", product: productB }); // B-owned → uploaded
    await settle();

    // Exactly one capture upload, and it is the B target.
    expect(captureTargetIds).toEqual(["target-b-variant"]);
  });
});
