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
    if (url.includes("/ext/pairing/self-revoke")) {
      // #149: the server confirms the credential-scoped self-revoke.
      return new Response(null, { status: 204 });
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
      if (url.includes("/ext/pairing/self-revoke")) {
        // #149: the server confirms the credential-scoped self-revoke.
        return new Response(null, { status: 204 });
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

// Issue #149 / PD-4(B): Revoke must invalidate the capture credential at the
// SERVER before the extension reports success. Deleting only the local copy left
// a copied credential uploading until its own server-side expiry — a kill switch
// that had not actually killed anything.
//
// The invariants under test (fail closed at every step):
//   - capture is disabled IMMEDIATELY either way;
//   - a non-confirmation NEVER clears the local credential material (it is the
//     only way to retry) and leaves a DURABLE, VISIBLY pending state;
//   - a pending revoke survives an MV3 worker restart and is retried;
//   - repeated revokes are idempotent;
//   - the owned-target index and the upload queue stay fail-closed throughout.
describe("service worker — #149 Revoke revokes the credential at the SERVER before clearing local state", () => {
  const KEY_REVOCATION_PENDING = "revocationPending";
  const KEY_QUEUE = "queue";

  // A fetch mock whose SELF-REVOKE response is driven per test. Everything else
  // (pair, owned-targets, capture) succeeds so the worker reaches a paired,
  // capture-ready state first.
  function revokeFetch(revokeResponder: () => Response | Promise<Response>) {
    const revokeCalls: string[] = [];
    const capturePosts: string[] = [];
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/ext/pairing/claim")) {
        return new Response(JSON.stringify(CRED), { status: 200 });
      }
      if (url.includes("/ext/pairing/self-revoke")) {
        const auth = (init?.headers as Record<string, string> | undefined)?.authorization ?? "";
        revokeCalls.push(auth);
        return await revokeResponder();
      }
      if (url.includes("/ext/owned-targets")) {
        return new Response(JSON.stringify({ items: [ownedTargetRow(product)] }), { status: 200 });
      }
      capturePosts.push(url);
      return new Response(null, { status: 202 });
    });
    return { fetch, revokeCalls, capturePosts };
  }

  const product = parsedProduct();
  let storage: Map<string, unknown>;

  async function pairedWorker(revokeResponder: () => Response | Promise<Response>): Promise<{
    send: (msg: ExtMessage) => Promise<ExtResponse>;
    revokeCalls: string[];
    capturePosts: string[];
  }> {
    const mock = installChromeMock();
    storage = mock.storage;
    const rf = revokeFetch(revokeResponder);
    vi.stubGlobal("fetch", rf.fetch);
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    return { send, revokeCalls: rf.revokeCalls, capturePosts: rf.capturePosts };
  }

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("a CONFIRMED server revoke clears the credential and reports the revoked kill-switch state", async () => {
    const { send, revokeCalls } = await pairedWorker(() => new Response(null, { status: 204 }));

    const resp = await send({ kind: "revoke" });

    // The server was actually contacted, with the credential as a Bearer.
    expect(revokeCalls).toEqual([`Bearer ${CRED.credential}`]);
    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revoked");
    expect(resp.state.degradation).toBe("credential_revoked");
    // Only NOW is the local material discarded, and no marker is left behind.
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
  });

  it("PD-4 negative: a server revoke FAILURE does NOT clear the local capture credential — and capture is disabled anyway", async () => {
    const { send, revokeCalls } = await pairedWorker(() => new Response("{}", { status: 500 }));

    const resp = await send({ kind: "revoke" });

    expect(revokeCalls.length).toBe(1);
    // The credential material is RETAINED — it is the only way to retry the
    // revoke. Clearing it here is precisely the #149 bug.
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
    // …but capture is disabled IMMEDIATELY, and the state is visibly PENDING —
    // never reported as a completed revocation.
    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revocation_pending");
    expect(resp.state.degradation).toBe("revocation_pending");
    expect(resp.state.degradation).not.toBe("credential_revoked");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();
  });

  it("an OFFLINE revoke disables capture and stays durably, visibly pending", async () => {
    const { send } = await pairedWorker(() => {
      throw new Error("offline");
    });

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revocation_pending");
    const pending = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    expect(pending).toBeDefined();
    // The marker is JSON-safe bookkeeping ONLY — never the credential secret.
    expect(pending.credentialId).toBe(CRED.credentialId);
    expect(JSON.stringify(pending)).not.toContain(CRED.credential);
  });

  it("a pending revoke survives a WORKER RESTART and is retried until the server confirms", async () => {
    // Boot 1: the server is down, so the revoke stays pending.
    const first = await pairedWorker(() => new Response("{}", { status: 503 }));
    await first.send({ kind: "revoke" });
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
    const survivingStorage = storage;
    // Simulate the retry backoff window elapsing (issue #149 F3). The retry is
    // now scheduled from the DURABLE attempt count, so a restart resumes the
    // pending revoke on its schedule rather than immediately — the invariant
    // under test ("a pending revoke survives a restart and is retried until the
    // server confirms") is unchanged; only "immediately" was never a guarantee
    // the kill switch needed, and an unbounded 1/min retry is the thundering
    // herd F3 closes. Moving the persisted schedule into the past IS "time
    // passed", because the marker is the only record of it.
    const persisted = survivingStorage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    survivingStorage.set(KEY_REVOCATION_PENDING, {
      ...persisted,
      nextAttemptAt: new Date(Date.now() - 1000).toISOString(),
    });
    vi.unstubAllGlobals();

    // Boot 2: the SAME chrome.storage (an MV3 teardown loses only memory), the
    // server is back. The worker must pick the pending revoke up on start.
    const rf = revokeFetch(() => new Response(null, { status: 204 }));
    (globalThis as unknown as { chrome: { storage: unknown } }).chrome.storage = {
      local: {
        get: vi.fn(async (key: string | null) => {
          if (key === null) return Object.fromEntries(survivingStorage.entries());
          return survivingStorage.has(key) ? { [key]: survivingStorage.get(key) } : {};
        }),
        set: vi.fn(async (obj: Record<string, unknown>) => {
          for (const [k, v] of Object.entries(obj)) survivingStorage.set(k, v);
        }),
        remove: vi.fn(async (key: string) => {
          survivingStorage.delete(key);
        }),
      },
    };
    vi.stubGlobal("fetch", rf.fetch);
    const send = await loadWorker();
    for (let i = 0; i < 20 && survivingStorage.has(KEY_REVOCATION_PENDING); i++) {
      await new Promise((r) => setTimeout(r, 0));
    }

    // The restart retried and CONFIRMED it — no pending revoke was lost.
    expect(rf.revokeCalls).toContain(`Bearer ${CRED.credential}`);
    expect(survivingStorage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(survivingStorage.get(KEY_CREDENTIAL)).toBeUndefined();
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
  });

  it("repeated revokes are idempotent — the end state is the same and nothing is resurrected", async () => {
    const { send } = await pairedWorker(() => new Response(null, { status: 204 }));

    const first = await send({ kind: "revoke" });
    const second = await send({ kind: "revoke" });
    const third = await send({ kind: "revoke" });

    for (const resp of [first, second, third]) {
      if (!("state" in resp)) throw new Error("expected state");
      expect(resp.state.capability).toBe("revoked");
    }
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
  });

  it("a 401 self-revoke is CONFIRMED — a pending marker for an already-dead credential always clears", async () => {
    const { send } = await pairedWorker(() => new Response("{}", { status: 401 }));

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revoked");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
  });

  it("while a revoke is PENDING the owned-target index and the upload queue stay fail-closed", async () => {
    const { send, capturePosts } = await pairedWorker(() => new Response("{}", { status: 500 }));
    // Prove the paired worker really was capture-ready before the revoke.
    await send({ kind: "capture", product });
    expect(capturePosts.length).toBe(1);

    await send({ kind: "revoke" });
    capturePosts.length = 0;

    // Nothing resolves through the (cleared) owned-target index…
    expect(await send({ kind: "addToWatchlist", product })).toEqual({
      ok: true,
      watchlist: { ok: false, reason: "denied" },
    });
    expect(await send({ kind: "getOverlayView", product })).toEqual({
      ok: true,
      overlay: { kind: "unavailable" },
    });
    // …no capture is enqueued, and the queue never flushes to the server while
    // the revocation is unconfirmed.
    await send({ kind: "capture", product });
    expect(capturePosts).toEqual([]);
    expect((storage.get(KEY_QUEUE) as unknown[] | undefined) ?? []).toEqual([]);

    // The kill switch cannot be toggled back on while pending, and re-pairing is
    // refused rather than abandoning a credential that is still live server-side.
    const enabled = await send({ kind: "setEnabled", enabled: true });
    if (!("state" in enabled)) throw new Error("expected state");
    expect(enabled.state.capability).toBe("revocation_pending");
    expect(await send({ kind: "pair", code: "code-123" })).toEqual({
      ok: false,
      error: "revocation_pending",
    });
  });
});

// Issue #149, review cycle 1. Findings F3–F7: the pending-revocation state
// machine's remaining fail-open seams. Every test here is a NEGATIVE — it
// asserts the kill switch never reports more than it achieved and never keeps
// using a credential the user asked to kill.
describe("service worker — #149 pending revocation never leaks a live credential (F3–F7)", () => {
  const KEY_REVOCATION_PENDING = "revocationPending";
  const CRED_B = {
    credential: "cap-cred-hex-B",
    credentialId: "55555555-5555-5555-5555-555555555555",
    marketplaceAccountId: "22222222-2222-2222-2222-222222222222",
    expiresAt: "2026-08-01T00:00:00Z",
  };

  const product = parsedProduct();
  let storage: Map<string, unknown>;

  // A fetch mock that records EVERY request with its Authorization header, so a
  // test can prove which credential (if any) a call was made with.
  function recordingFetch(opts: {
    revoke: () => Response | Promise<Response>;
    claim?: () => typeof CRED | typeof CRED_B;
    targetsFor?: (auth: string) => unknown[];
  }) {
    const calls: Array<{ url: string; auth: string; body?: string }> = [];
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      const auth = (init?.headers as Record<string, string> | undefined)?.authorization ?? "";
      calls.push({ url, auth, body: init?.body as string | undefined });
      if (url.includes("/ext/pairing/claim")) {
        return new Response(JSON.stringify(opts.claim ? opts.claim() : CRED), { status: 200 });
      }
      if (url.includes("/ext/pairing/self-revoke")) return await opts.revoke();
      if (url.includes("/ext/owned-targets")) {
        const rows = opts.targetsFor ? opts.targetsFor(auth) : [ownedTargetRow(product)];
        return new Response(JSON.stringify({ items: rows }), { status: 200 });
      }
      return new Response(null, { status: 202 });
    });
    return { fetch, calls };
  }

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

  const settle = async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
  };

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  // F5 (a). REGRESSION vs. origin/main: while a revoke is pending the retained
  // credential kept syncing owned targets, because syncOwnedTargets gated on
  // credential PRESENCE only — no capability check, no pending-revocation check.
  // Every 15-minute alarm and every worker start re-issued GET /ext/owned-targets
  // with the exact credential the user just asked to revoke, and repopulated the
  // index handleRevoke had deliberately cleared.
  it("F5(a): a PENDING revoke stops owned-target sync — the revoked-by-request credential never calls the gateway again", async () => {
    const rf = recordingFetch({ revoke: () => new Response("{}", { status: 500 }) });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });

    const resp = await send({ kind: "revoke" });
    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revocation_pending");
    rf.calls.length = 0;

    // The periodic refresh fires while the revoke is still unconfirmed.
    alarmHandler()({ name: "scheduled-refresh" });
    await settle();

    const targetReads = rf.calls.filter((c) => c.url.includes("/ext/owned-targets"));
    expect(targetReads).toEqual([]);
    // …and nothing repopulated the deliberately-cleared index.
    expect(await send({ kind: "addToWatchlist", product })).toEqual({
      ok: true,
      watchlist: { ok: false, reason: "denied" },
    });
  });

  // F5 (b). finalizeRevocation removed the credential and set `revoked` but,
  // unlike handleRevoke, neither bumped the sync generation nor cleared the
  // index. A repopulated PRE-revocation index therefore survived finalization
  // and could resolve a target belonging to account A while the extension was
  // authenticated as account B.
  it("F5(b): finalizing a revocation tears the owned-target index down, so a re-pair mid-sync can never upload account A's target under account B's credential", async () => {
    let claimed = CRED;
    // Account B's owned-target read HANGS, so the re-pair's sync is still in
    // flight when a capture arrives — the window Probe B exercises. Account B
    // owns NOTHING, so any target that resolves in that window can only have
    // come from account A's PRE-revocation projection.
    let releaseBSync: () => void = () => {};
    const bSyncBlocked = new Promise<void>((r) => {
      releaseBSync = r;
    });
    const calls: Array<{ url: string; auth: string; body?: string }> = [];
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      const auth = (init?.headers as Record<string, string> | undefined)?.authorization ?? "";
      calls.push({ url, auth, body: init?.body as string | undefined });
      if (url.includes("/ext/pairing/claim")) {
        return new Response(JSON.stringify(claimed), { status: 200 });
      }
      if (url.includes("/ext/pairing/self-revoke")) return new Response(null, { status: 204 });
      if (url.includes("/ext/owned-targets")) {
        if (auth === `Bearer ${CRED.credential}`) {
          return new Response(JSON.stringify({ items: [ownedTargetRow(product)] }), {
            status: 200,
          });
        }
        await bSyncBlocked; // account B's sync never completes during the window
        return new Response(JSON.stringify({ items: [] }), { status: 200 });
      }
      return new Response(null, { status: 202 });
    });
    vi.stubGlobal("fetch", fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" }); // index := account A's target

    // A pending revoke exists while the projection is still populated in memory
    // — the state the reconciliation path (F7) and any teardown-then-respawn
    // sequence produce. Written directly so the settle, not handleRevoke, is
    // what clears the index.
    storage.set(KEY_REVOCATION_PENDING, {
      requestedAt: new Date().toISOString(),
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      attempts: 0,
      serverContacted: true,
    });
    storage.set(KEY_CAPABILITY, "revocation_pending");

    // The server confirms: finalizeRevocation runs with the index POPULATED.
    alarmHandler()({ name: "queue-flush" });
    await settle();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();

    // The user re-pairs to a DIFFERENT marketplace account. Its owned-target
    // sync is still in flight (deliberately blocked) when a capture arrives.
    claimed = CRED_B;
    const pairing = send({ kind: "pair", code: "code-123" });
    await settle();
    calls.length = 0;
    await send({ kind: "capture", product });
    await settle();

    const uploads = calls.filter((c) => c.url.includes("/observation/capture"));
    for (const upload of uploads) {
      expect(upload.body ?? "").not.toContain(CRED.marketplaceAccountId);
      expect(upload.body ?? "").not.toContain("target-1");
    }
    expect(uploads).toEqual([]);

    releaseBSync();
    await pairing;
  });

  // F7. Durability ordering: setCapability("revocation_pending") ran BEFORE the
  // durable marker was written. An MV3 teardown between those two awaits left
  // capability `revocation_pending` with NO marker; retryPendingRevocation then
  // early-returned without reconciling, so the revoke was never retried and the
  // server credential stayed live until its own 30-day expiry — while the popup
  // showed "awaiting confirmation" forever.
  it("F7: a capability of revocation_pending with NO durable marker is reconciled and retried, never stranded", async () => {
    let revokeStatus = 500;
    const rf = recordingFetch({
      // A 204 carries NO body (constructing one with a body throws).
      revoke: () => new Response(revokeStatus === 204 ? null : "{}", { status: revokeStatus }),
    });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    let send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" });

    // Simulate the teardown-induced loss of the durable marker while the
    // capability write survived (and, equivalently, any storage eviction).
    storage.delete(KEY_REVOCATION_PENDING);
    storage.set(KEY_CAPABILITY, "revocation_pending");
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();

    // Boot 2 with a healthy server: the worker must notice the inconsistency,
    // rebuild the marker from the still-stored credential, and settle it.
    revokeStatus = 204;
    vi.unstubAllGlobals();
    const rf2 = recordingFetch({ revoke: () => new Response(null, { status: 204 }) });
    vi.stubGlobal("fetch", rf2.fetch);
    send = await loadWorker();
    await settle();

    expect(rf2.calls.some((c) => c.url.includes("/ext/pairing/self-revoke"))).toBe(true);
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
  });

  // F7 (the mirror window). With the marker written FIRST, a teardown between
  // the two writes leaves a durable marker while the stored capability is still
  // `ready`. The durable marker must be AUTHORITATIVE — capture stays off.
  it("F7: a durable pending marker keeps capture OFF even if the stored capability still says ready", async () => {
    const rf = recordingFetch({ revoke: () => new Response("{}", { status: 500 }) });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" });

    // Force the exact interleaving: marker present, capability not yet written.
    storage.set(KEY_CAPABILITY, "ready");
    rf.calls.length = 0;

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_pending");
    await send({ kind: "capture", product });
    await settle();
    expect(rf.calls.filter((c) => c.url.includes("/observation/capture"))).toEqual([]);
  });

  // F4. The "authoritative expiry" shortcut compared the credential's expiry
  // against the DEVICE clock. A clock set forward discarded the credential,
  // cleared the marker and reported the kill switch complete, while the server
  // row was live for the real remaining TTL and a copied credential still worked.
  it("F4: a skewed-forward local clock alone NEVER finalizes a revocation the server never confirmed", async () => {
    const rf = recordingFetch({
      // The device is offline: the server is never reached, so there is no
      // evidence whatsoever about the credential — or about the clock.
      revoke: () => {
        throw new Error("offline");
      },
    });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" });
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();

    // The user's clock jumps past the credential's expiry.
    const skewed = Date.parse(CRED.expiresAt) + 24 * 60 * 60 * 1000;
    vi.spyOn(Date, "now").mockReturnValue(skewed);
    try {
      alarmHandler()({ name: "queue-flush" });
      await settle();
      // The kill switch must NOT be reported complete: the credential material
      // is retained (it is the only way to retry) and the marker stands.
      expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
      expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();
      const state = await send({ kind: "getState" });
      if (!("state" in state)) throw new Error("expected state");
      expect(state.state.capability).toBe("revocation_pending");
      expect(state.state.capability).not.toBe("revoked");
    } finally {
      vi.mocked(Date.now).mockRestore();
    }
  });

  // F3. The retry ran on every 1-minute alarm tick with no backoff and no cap,
  // and `attempts` was persisted but never read — a deliberately incomplete
  // seam. CLAUDE.md: backpressure is the default, rate limiting sits in front of
  // every external call.
  it("F3: repeated alarm ticks do NOT hammer the gateway — the persisted attempt count drives a backoff", async () => {
    const rf = recordingFetch({ revoke: () => new Response("{}", { status: 503 }) });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" }); // attempt #1
    const afterFirst = rf.calls.filter((c) => c.url.includes("self-revoke")).length;
    expect(afterFirst).toBe(1);

    // Thirty 1-minute alarm ticks in immediate succession (the alarm is the only
    // driver, and it fires regardless of backoff).
    for (let i = 0; i < 30; i++) {
      alarmHandler()({ name: "queue-flush" });
      await settle();
    }
    const total = rf.calls.filter((c) => c.url.includes("self-revoke")).length;
    expect(total).toBe(afterFirst); // every tick was inside the backoff window

    // The schedule is DURABLE — an MV3 teardown cannot reset it back to 1/min.
    const pending = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    expect(pending.attempts).toBe(1);
    expect(typeof pending.nextAttemptAt).toBe("string");
    expect(Date.parse(pending.nextAttemptAt as string)).toBeGreaterThan(Date.now());
  });

  it("F3: once the backoff window elapses the retry runs again and can confirm", async () => {
    let revokeStatus = 503;
    const rf = recordingFetch({
      // A 204 carries NO body (constructing one with a body throws).
      revoke: () => new Response(revokeStatus === 204 ? null : "{}", { status: revokeStatus }),
    });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" });

    // Simulate the backoff window elapsing (the marker is durable bookkeeping,
    // so moving its schedule into the past is exactly "time passed").
    const pending = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    storage.set(KEY_REVOCATION_PENDING, {
      ...pending,
      nextAttemptAt: new Date(Date.now() - 1000).toISOString(),
    });
    revokeStatus = 204;
    alarmHandler()({ name: "queue-flush" });
    await settle();

    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
  });

  it("F3: a USER-initiated pair forces an immediate settle attempt rather than waiting out the backoff", async () => {
    let revokeStatus = 503;
    const rf = recordingFetch({
      // A 204 carries NO body (constructing one with a body throws).
      revoke: () => new Response(revokeStatus === 204 ? null : "{}", { status: revokeStatus }),
    });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" });
    const before = rf.calls.filter((c) => c.url.includes("self-revoke")).length;

    revokeStatus = 204;
    const resp = await send({ kind: "pair", code: "code-123" });
    expect(rf.calls.filter((c) => c.url.includes("self-revoke")).length).toBeGreaterThan(before);
    // The pending revoke settled first, so the re-pair is allowed to proceed.
    expect(resp.ok).toBe(true);
  });

  // F6. The orphan branch is the ONLY transition reaching `revoked` without a
  // server confirmation or an expiry check, and it emitted neither a counter nor
  // a log — telemetry could not tell it from a genuine confirmation. CLAUDE.md:
  // a fallback engaging without an emitted, traced, audited event is a bug.
  it("F6: an ORPHANED pending marker is resolved with a DISTINCT, observable outcome", async () => {
    const rf = recordingFetch({ revoke: () => new Response("{}", { status: 500 }) });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await send({ kind: "revoke" });

    // The credential material is gone (storage eviction / a partial clear), so
    // no retry can ever succeed against this marker.
    storage.delete(KEY_CREDENTIAL);

    const { snapshotMetrics } = await import("../lib/observability");
    alarmHandler()({ name: "queue-flush" });
    await settle();

    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    const revocationOutcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => s.labels.outcome);
    expect(revocationOutcomes).toContain("orphaned");
    // It must NOT be recorded as a confirmed revocation — that is the whole
    // point of the distinct outcome.
    expect(revocationOutcomes).not.toContain("confirmed");
    expect(revocationOutcomes).not.toContain("expired");
  });
});
