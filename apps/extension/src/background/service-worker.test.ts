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

// The unconfirmed-revocation QUARANTINE store is a BOUNDED LIST keyed by
// credentialId (#149): a second unconfirmable revocation must never evict the
// first. A legacy single-record snapshot (written by an earlier build, or seeded
// by a test as such a build would have) reads as a one-element list, so this
// helper reads EITHER durable shape.
function quarantineRecords(storage: Map<string, unknown>): Array<Record<string, unknown>> {
  const raw = storage.get("revocationUnconfirmed");
  if (raw === undefined || raw === null) return [];
  return (Array.isArray(raw) ? raw : [raw]) as Array<Record<string, unknown>>;
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
  // The MOST RECENTLY registered listener — i.e. the worker instance that was
  // just (re-)imported. A restart test drives the RESPAWNED worker; taking
  // calls[0] would keep talking to the pre-teardown instance (and its surviving
  // in-memory state), which is precisely what such a test must not do.
  const handler = chromeMock.runtime.onMessage.addListener.mock.calls.at(-1)?.[0] as (
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

  // Issue #149, fix 3 REVERSES the earlier reading of this case. A bare 401 is
  // not proof of anything: a probe showed an UNMOUNTED route and a genuine
  // authoritative revocation returning byte-identical
  // `401 {"code":"NO_SESSION"}`. Only the authority's OWN
  // CAPTURE_CREDENTIAL_INVALID verdict clears a marker for an already-dead
  // credential — which is what keeps a repeated revoke idempotent.
  it("an AUTHORITATIVE 401 is CONFIRMED — a marker for an already-dead credential always clears", async () => {
    const { send } = await pairedWorker(
      () =>
        new Response(JSON.stringify({ code: "CAPTURE_CREDENTIAL_INVALID", message: "not valid" }), {
          status: 401,
        }),
    );

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revoked");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
  });

  it("NEGATIVE: an UNQUALIFIED 401 is NOT confirmation — it clears neither the marker nor the credential", async () => {
    const { send } = await pairedWorker(() => new Response("{}", { status: 401 }));

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revocation_pending");
    expect(resp.state.capability).not.toBe("revoked");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
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
    // G2: rebuilding a marker is a local reconstruction, not an authoritative
    // event — it must be counted under its OWN outcome so telemetry can tell
    // the reconciliation apart from the confirmation that followed it.
    const { snapshotMetrics } = await import("../lib/observability");
    const outcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
    expect(outcomes).toContain("marker_reconstructed");
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
  });

  // F7 (the stranded-UI case). A capability of `revocation_pending` with NEITHER
  // a marker NOR credential material can never be retried and can never clear on
  // its own, so the popup would report "awaiting confirmation" forever. Resolve
  // it fail-closed and OBSERVABLY, exactly like the orphan branch.
  it("F7: a revocation_pending capability with no marker AND no credential resolves instead of stranding the popup", async () => {
    const rf = recordingFetch({ revoke: () => new Response(null, { status: 204 }) });
    vi.stubGlobal("fetch", rf.fetch);
    storage = installChromeMock().storage;
    storage.set(KEY_CAPABILITY, "revocation_pending");

    const send = await loadWorker();
    await settle();
    // Imported AFTER loadWorker: it resets the module registry, so this must
    // resolve to the SAME observability instance the worker just used.
    const { snapshotMetrics } = await import("../lib/observability");

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
    expect(state.state.capability).not.toBe("revocation_pending");
    const outcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => s.labels.outcome);
    expect(outcomes).toContain("orphaned");
    expect(outcomes).not.toContain("confirmed");
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
      // is RETAINED (it is the only way to retry) and the revoke is still being
      // pursued.
      //
      // A skew this large (past the credential's expiry, hence well past the
      // marker's age bound) legitimately moves the revoke into the "could not
      // confirm" QUARANTINE (#149 fix 3), which is why the material may live in
      // either home. What must NEVER happen — and is what F4 exists for — is a
      // clock-driven terminal `revoked`, or the credential being discarded.
      const material =
        (storage.get(KEY_CREDENTIAL) as { credential?: string } | undefined)?.credential ??
        (quarantineRecords(storage)[0] as { credential?: string } | undefined)?.credential;
      expect(material).toBe(CRED.credential);
      const state = await send({ kind: "getState" });
      if (!("state" in state)) throw new Error("expected state");
      expect(["revocation_pending", "revocation_unconfirmed"]).toContain(state.state.capability);
      expect(state.state.capability).not.toBe("revoked");
      // G2: the refusal is OBSERVABLE. A credential-discarding decision that
      // emits nothing is an unproven seam (CLAUDE.md: a fallback engaging
      // without an emitted event is always a bug).
      const { snapshotMetrics } = await import("../lib/observability");
      const outcomes = snapshotMetrics()
        .filter((s) => s.name === "credential_revocation")
        .map((s) => String(s.labels.outcome));
      expect(outcomes).toContain("expiry_unverified");
      expect(outcomes).not.toContain("expired_local_clock");
      expect(outcomes).not.toContain("confirmed");
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

// Issue #149, review cycle 2. Findings G1–G5: every remaining branch that
// reaches a TERMINAL state without an authoritative server answer. The class
// under test is one bug on five branches — a locally-resolved revocation that is
// neither observable nor tested. Each test below is a NEGATIVE first: the kill
// switch must never claim more than the authority confirmed, must never promote
// a capability upward, and must never let a local resolution look like a
// confirmation in telemetry.
describe("service worker — #149 cycle 2: a locally-resolved revocation is never a confirmed one (G1–G5)", () => {
  const KEY_REVOCATION_PENDING = "revocationPending";
  const KEY_TELEMETRY_OUTBOX = "telemetryOutbox";
  const product = parsedProduct();
  let storage: Map<string, unknown>;

  const settle = async () => {
    for (let i = 0; i < 10; i++) await new Promise((r) => setTimeout(r, 0));
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

  // A fetch mock that counts self-revoke requests and answers them with a
  // per-test status, so a test can prove an attempt was (or was not) MADE —
  // the core of G1: an expiry shortcut must never skip an available authority.
  function revokeFetch(status: () => number) {
    const revokeCalls: string[] = [];
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/ext/pairing/claim")) {
        return new Response(JSON.stringify(CRED), { status: 200 });
      }
      if (url.includes("/ext/pairing/self-revoke")) {
        const auth = (init?.headers as Record<string, string> | undefined)?.authorization ?? "";
        revokeCalls.push(auth);
        const s = status();
        return new Response(s === 204 ? null : "{}", { status: s });
      }
      if (url.includes("/ext/owned-targets")) {
        return new Response(JSON.stringify({ items: [ownedTargetRow(product)] }), { status: 200 });
      }
      return new Response(null, { status: 202 });
    });
    return { fetch, revokeCalls };
  }

  // Seeds the exact durable state an MV3 worker would find on a cold start with
  // an unconfirmed revocation outstanding: a credential, the pending capability,
  // and a durable marker whose retry is already due.
  function seedPending(over: Record<string, unknown> = {}): void {
    storage.set(KEY_CREDENTIAL, CRED);
    storage.set(KEY_CAPABILITY, "revocation_pending");
    storage.set(KEY_REVOCATION_PENDING, {
      // A RECENT request: these tests are about a revoke still in progress, not
      // about the marker's age bound (#149 fix 3, covered by its own tests). A
      // fixed literal here would silently age past that bound as time passes.
      requestedAt: new Date().toISOString(),
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      attempts: 1,
      serverContacted: true,
      nextAttemptAt: "2020-01-01T00:00:00Z", // already due
      ...over,
    });
  }

  async function revocationOutcomes(): Promise<string[]> {
    const { snapshotMetrics } = await import("../lib/observability");
    return snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
  }

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // G1. The expiry shortcut was evaluated BEFORE the request, so a device clock
  // pushed past the credential's expiry finalized the revoke as `revoked`
  // without ever asking the authority that was sitting there ready to answer.
  // `serverContacted` did not save it: ANY real HTTP response sets that flag
  // (a single 503 during a deploy sets it forever), and reaching the gateway is
  // not evidence the clock is right.
  it("G1: a skewed-forward clock never SKIPS an available authority — the attempt runs first and wins", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 204);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending();
    // The device clock is a full day past the credential's expiry.
    vi.spyOn(Date, "now").mockReturnValue(Date.parse(CRED.expiresAt) + 24 * 60 * 60 * 1000);

    const send = await loadWorker();
    await settle();

    // The authoritative answer was ASKED FOR — the previous code made ZERO calls.
    expect(rf.revokeCalls).toEqual([`Bearer ${CRED.credential}`]);
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
    const outcomes = await revocationOutcomes();
    // …and it is recorded as what it is: a real server confirmation.
    expect(outcomes).toContain("confirmed");
    expect(outcomes).not.toContain("expired_local_clock");
  });

  // G1. The other half: a skewed clock plus a NON-authoritative answer must
  // leave the revoke visibly pending with its credential intact, never a
  // terminal `revoked` the server never agreed to.
  it("G1: a skewed-forward clock plus a non-authoritative answer NEVER finalizes as revoked", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 503);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending();
    vi.spyOn(Date, "now").mockReturnValue(Date.parse(CRED.expiresAt) + 24 * 60 * 60 * 1000);

    const send = await loadWorker();
    await settle();

    expect(rf.revokeCalls.length).toBe(1);
    // The credential material is the ONLY thing a retry can be made with, so it
    // is RETAINED — in the pending marker's home, or (once a skew this large
    // trips the marker's age bound, #149 fix 3) in the quarantine record. The
    // invariant under test is that neither the clock nor a non-authoritative
    // answer ever reaches a terminal `revoked`.
    const material =
      (storage.get(KEY_CREDENTIAL) as { credential?: string } | undefined)?.credential ??
      (quarantineRecords(storage)[0] as { credential?: string } | undefined)?.credential;
    expect(material).toBe(CRED.credential);
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(["revocation_pending", "revocation_unconfirmed"]).toContain(state.state.capability);
    expect(state.state.capability).not.toBe("revoked");
    const outcomes = await revocationOutcomes();
    // G2: the refused-expiry branch is OBSERVABLE and distinct.
    expect(outcomes).toContain("expiry_unverified");
    expect(outcomes).not.toContain("expired_local_clock");
    expect(outcomes).not.toContain("confirmed");
  });

  // G1 (the marker's lifetime bound). A pending marker may not live forever, but
  // the bound must be CLOCK-INDEPENDENT and must terminate into a state that is
  // NOT `revoked` — the popup may never claim a kill switch the authority never
  // confirmed. `unknown` (not_paired) is honest: capture is off, nothing is
  // claimed about the server row, and the user can pair again.
  //
  // #149 fix 3 changes the TERMINAL, not the invariant. The budget used to end
  // in `unknown` with the credential DISCARDED, which defeated EXT-009: the
  // server row could still be live and the only material a retry could use was
  // destroyed. It now ends in the explicit "could not confirm" QUARANTINE, which
  // retains the material, keeps retrying, and is still NEVER `revoked`.
  it("G1: the clock-independent attempt budget terminates in QUARANTINE, NEVER `revoked`", async () => {
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 503);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({ attempts: REVOCATION_MAX_ATTEMPTS - 1 });

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.capability).not.toBe("revoked");
    expect(state.state.degradation).toBe("revocation_unconfirmed");
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    // Relocated, NOT destroyed — that is the whole point of the quarantine.
    expect((quarantineRecords(storage)[0] as { credential?: string } | undefined)?.credential).toBe(
      CRED.credential,
    );
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("quarantined_unconfirmed");
    expect(outcomes).not.toContain("confirmed");
    expect(outcomes).not.toContain("expired_local_clock");
  });

  // G1 (RESIDUAL, stated and pinned). The device clock still gates WHEN a retry
  // becomes due (revocationRetryDue), so a forward-skewed clock can burn the
  // attempt budget faster than real time would. This test pins the boundary of
  // that residual: acceleration can only ever reach the QUARANTINE terminal —
  // there is no clock-influenced path to a terminal `revoked` at all.
  it("G1 residual: a skewed clock can only ACCELERATE exhaustion into QUARANTINE, never reach `revoked`", async () => {
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 503);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({
      attempts: REVOCATION_MAX_ATTEMPTS - 1,
      // Not due for years by a correct clock…
      nextAttemptAt: "2030-01-01T00:00:00Z",
    });
    // …but the device clock says it is long past, AND past the expiry.
    vi.spyOn(Date, "now").mockReturnValue(Date.parse("2031-01-01T00:00:00Z"));

    const send = await loadWorker();
    await settle();

    expect(rf.revokeCalls.length).toBe(1); // the skew let the attempt run early
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.capability).not.toBe("revoked");
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("quarantined_unconfirmed");
    expect(outcomes).not.toContain("confirmed");
  });

  // G3. The flush alarm ran the durable upload-queue flush DOWNSTREAM of the
  // revocation retry with no `.catch`. retryPendingRevocation writes to
  // chrome.storage, and chrome.storage.local.set CAN reject (QUOTA_BYTES). The
  // rejection escaped the `void`-ed chain, so flush() and pumpTelemetry() never
  // ran for that tick — and because the failing write is deterministic, the
  // durable upload queue stopped draining indefinitely with no observable
  // signal. That also inverts CLAUDE.md's load-shedding priority: the
  // revocation path must never starve the upload path.
  it("G3: a storage failure inside the revocation retry never kills flush/telemetry, and is observable", async () => {
    const mock = installChromeMock();
    storage = mock.storage;
    const rf = revokeFetch(() => 503);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({ attempts: 0, serverContacted: false });

    const send = await loadWorker();
    await settle();

    // Move the durable retry schedule into the past so the tick below actually
    // ATTEMPTS (otherwise it defers inside the backoff window and never writes).
    const marker = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    storage.set(KEY_REVOCATION_PENDING, {
      ...marker,
      nextAttemptAt: new Date(Date.now() - 1000).toISOString(),
    });

    // The marker write now fails deterministically, exactly like a quota error.
    const chromeMock = (
      globalThis as unknown as {
        chrome: { storage: { local: { set: ReturnType<typeof vi.fn> } } };
      }
    ).chrome;
    chromeMock.storage.local.set.mockImplementation(async (obj: Record<string, unknown>) => {
      if (KEY_REVOCATION_PENDING in obj) throw new Error("QUOTA_BYTES quota exceeded");
      for (const [k, v] of Object.entries(obj)) storage.set(k, v);
    });
    storage.delete(KEY_TELEMETRY_OUTBOX);

    alarmHandler()({ name: "queue-flush" });
    await settle();

    // The downstream chain still ran for this tick — the telemetry pump sits
    // AFTER flush(), so a persisted batch proves flush() was not skipped.
    expect(storage.get(KEY_TELEMETRY_OUTBOX)).toBeDefined();
    // …and the failure is a counted, distinct outcome, never a silent swallow.
    expect(await revocationOutcomes()).toContain("retry_error");
    void send;
  });

  // G4. The orphan branch repaired the capability only when it was LITERALLY
  // `revocation_pending`. Compose the two teardown windows the F6/F7 tests
  // already establish (marker durable while the capability write was lost;
  // credential material evicted) and the marker is removed while the stored
  // capability stays `ready` — so right after a user revoke the popup renders
  // capture ON with no degradation note, and nav-shim injection resumes on a
  // just-revoked, unpaired extension.
  it("G4: an orphan resolution NEVER leaves a stored `ready` capability behind", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 500);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending();
    // The credential material is gone (eviction / a partial clear)…
    storage.delete(KEY_CREDENTIAL);
    // …and the capability write of the revoke was lost to a teardown.
    storage.set(KEY_CAPABILITY, "ready");

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).not.toBe("ready");
    expect(state.state.capability).toBe("revoked");
    expect(state.state.degradation).toBe("credential_revoked");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(await revocationOutcomes()).toContain("orphaned");
  });

  // G4 (the other half of "never promote"). The repair may only ever make the
  // capability MORE restrictive. A user-disabled extension whose orphaned marker
  // is cleaned up stays DISABLED — the repair is not licence to rewrite an
  // unrelated state.
  it("G4: the orphan repair never rewrites unknown/disabled — it only refuses to promote", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 500);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending();
    storage.delete(KEY_CREDENTIAL);
    storage.set(KEY_CAPABILITY, "disabled");

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("disabled");
    expect(state.state.capability).not.toBe("ready");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
  });

  // G5. handleRevoke's no-credential branch discarded a DURABLE pending marker
  // and reported a completed kill switch with NO metric at all, while the
  // timer-driven sibling emitted `orphaned` for the identical situation.
  // Telemetry could not distinguish a user-initiated orphan resolution from a
  // genuine confirmed revocation.
  it("G5: a user revoke that finds a live marker but no credential is ORPHANED, never confirmed", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 500);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending();

    // Boot with the credential still present, so the timer-driven path settles
    // to `pending` — it must NOT be the thing that emits `orphaned` here. Only
    // then does the credential vanish, so the USER-driven branch under test is
    // the sole possible source of the outcome.
    const send = await loadWorker();
    await settle();
    expect(await revocationOutcomes()).not.toContain("orphaned");
    storage.delete(KEY_CREDENTIAL);
    const revokesBefore = rf.revokeCalls.length;

    const { snapshotMetrics } = await import("../lib/observability");
    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revoked");
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    // No server round-trip was possible — there was nothing to present.
    expect(rf.revokeCalls.length).toBe(revokesBefore);
    const outcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
    expect(outcomes).toContain("orphaned");
    expect(outcomes).not.toContain("confirmed");
    const transitions = snapshotMetrics().filter(
      (s) => s.name === "capability_transition" && s.labels.to === "revoked",
    );
    expect(transitions.length).toBeGreaterThan(0);
  });

  // G5 (the idempotent repeat). Revoking again after a COMPLETED revoke has no
  // marker and no credential. It is still a terminal `revoked`, so it still gets
  // its own bounded, documented outcome — but a distinct one, so a repeat can
  // never be mistaken for a genuine orphan resolution.
  it("G5: a repeat revoke with no marker and no credential is `already_cleared`, never `orphaned`", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => 204);
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CAPABILITY, "revoked");

    const send = await loadWorker();
    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revoked");
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("already_cleared");
    expect(outcomes).not.toContain("orphaned");
    expect(outcomes).not.toContain("confirmed");
  });
});

// Issue #149, fix 3. Two product-owner decisions:
//   (a) an explicit, observable, audited "could not confirm" QUARANTINE
//       terminal — never a silent discard of the credential (which defeats
//       EXT-009 outright) and never a permanent block on re-pairing;
//   (b) POSITIVE PROOF of revocation — only a 204 or the authority's own
//       CAPTURE_CREDENTIAL_INVALID verdict confirms. An unmounted route, a
//       404/405, or a generic proxy / pre-rollout 401 routes to (a).
//
// NEGATIVES FIRST: nothing here may ever reach a terminal `revoked` without
// positive proof (§4.6 quarantine over inference).
describe("service worker — #149 fix 3: unconfirmed revocations are QUARANTINED, never assumed", () => {
  const KEY_REVOCATION_PENDING = "revocationPending";
  const KEY_REVOCATION_UNCONFIRMED = "revocationUnconfirmed";
  const KEY_REVOCATION_ABANDONED = "revocationAbandoned";
  const product = parsedProduct();
  let storage: Map<string, unknown>;

  const settle = async () => {
    for (let i = 0; i < 30; i++) await new Promise((r) => setTimeout(r, 0));
  };

  // Gateway responses that are NOT positive proof of revocation.
  const genericProxy401 = () =>
    new Response(JSON.stringify({ code: "NO_SESSION", message: "authentication required" }), {
      status: 401,
    });
  const unmountedRoute = () => new Response("{}", { status: 404 });
  // The authority's OWN verdict — the only 401 that is proof.
  const authoritative401 = () =>
    new Response(JSON.stringify({ code: "CAPTURE_CREDENTIAL_INVALID", message: "not valid" }), {
      status: 401,
    });

  function revokeFetch(responder: () => Response | Promise<Response>) {
    const revokeCalls: string[] = [];
    const otherCalls: string[] = [];
    const fetch = vi.fn(async (input: string, init?: RequestInit) => {
      const url = String(input);
      if (url.includes("/ext/pairing/claim")) {
        return new Response(JSON.stringify(CRED), { status: 200 });
      }
      if (url.includes("/ext/pairing/self-revoke")) {
        revokeCalls.push(
          (init?.headers as Record<string, string> | undefined)?.authorization ?? "",
        );
        return await responder();
      }
      if (url.includes("/ext/owned-targets")) {
        otherCalls.push(url);
        return new Response(JSON.stringify({ items: [ownedTargetRow(product)] }), { status: 200 });
      }
      otherCalls.push(url);
      return new Response(null, { status: 202 });
    });
    return { fetch, revokeCalls, otherCalls };
  }

  function seedPending(over: Record<string, unknown> = {}): void {
    storage.set(KEY_CREDENTIAL, CRED);
    storage.set(KEY_CAPABILITY, "revocation_pending");
    storage.set(KEY_REVOCATION_PENDING, {
      requestedAt: new Date().toISOString(),
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      attempts: 1,
      serverContacted: true,
      nextAttemptAt: "2020-01-01T00:00:00Z", // already due
      ...over,
    });
  }

  async function revocationOutcomes(): Promise<string[]> {
    const { snapshotMetrics } = await import("../lib/observability");
    return snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
  }

  // The COUNT for one revocation outcome, not merely its presence: a per-read
  // increment and a once-per-persistence increment are indistinguishable
  // otherwise.
  async function revocationOutcomeCount(outcome: string): Promise<number> {
    const { snapshotMetrics } = await import("../lib/observability");
    return snapshotMetrics()
      .filter((s) => s.name === "credential_revocation" && s.labels.outcome === outcome)
      .reduce((n, s) => n + s.value, 0);
  }

  async function capabilityTransitions(): Promise<string[]> {
    const { snapshotMetrics } = await import("../lib/observability");
    return snapshotMetrics()
      .filter((s) => s.name === "capability_transition")
      .map((s) => String(s.labels.to));
  }

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // ---- (b) positive proof: the deploy-window 401 and the unmounted route ----

  it("Q1 NEGATIVE: a GENERIC proxy/pre-rollout 401 NEVER completes the revoke — the credential is retained and it stays pending", async () => {
    const mock = installChromeMock();
    storage = mock.storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revocation_pending");
    expect(resp.state.capability).not.toBe("revoked");
    // The credential material is RETAINED — the server row may well be live.
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();
    expect(await revocationOutcomes()).not.toContain("confirmed");
  });

  it("Q2 NEGATIVE: a 404 (route not mounted on this gateway build) NEVER completes the revoke", async () => {
    const mock = installChromeMock();
    storage = mock.storage;
    const rf = revokeFetch(unmountedRoute);
    vi.stubGlobal("fetch", rf.fetch);
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revocation_pending");
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
    expect(await revocationOutcomes()).not.toContain("confirmed");
  });

  it("Q3 POSITIVE: the authority's own CAPTURE_CREDENTIAL_INVALID verdict IS a confirmation (repeat revoke stays idempotent)", async () => {
    const mock = installChromeMock();
    storage = mock.storage;
    const rf = revokeFetch(authoritative401);
    vi.stubGlobal("fetch", rf.fetch);
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });

    const resp = await send({ kind: "revoke" });

    if (!("state" in resp)) throw new Error("expected state");
    expect(resp.state.capability).toBe("revoked");
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeUndefined();
    expect(await revocationOutcomes()).toContain("confirmed");
  });

  // ---- (a) the quarantine terminal ----

  it("Q4: an exhausted budget against a GENERIC 401 lands in the QUARANTINE terminal — never `revoked`, never a silent discard", async () => {
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({ attempts: REVOCATION_MAX_ATTEMPTS - 1 });

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.capability).not.toBe("revoked");
    expect(state.state.degradation).toBe("revocation_unconfirmed");
    // The credential material is NOT destroyed — it is RELOCATED into the
    // durable quarantine record, which is what keeps the retry possible.
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    const q = quarantineRecords(storage)[0] as Record<string, unknown>;
    expect(q).toBeDefined();
    expect(q.credential).toBe(CRED.credential);
    expect(q.credentialId).toBe(CRED.credentialId);
    expect(q.evidence).toBe("unconfirmed_generic_401");
    // AUDITED: its own metric outcome + capability transition, never folded into
    // `revoked` or a generic failure.
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("quarantined_unconfirmed");
    expect(outcomes).not.toContain("confirmed");
    expect(await capabilityTransitions()).toContain("revocation_unconfirmed");
    // The popup surfaces it as an outstanding, visible fact.
    expect(state.state.revocationUnconfirmed).toBe(true);
  });

  it("Q5: TRANSPORT-failure exhaustion (fully offline) also quarantines — the previously untested path", async () => {
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => {
      throw new Error("offline");
    });
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({ attempts: REVOCATION_MAX_ATTEMPTS - 1, serverContacted: false });

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.capability).not.toBe("revoked");
    const q = quarantineRecords(storage)[0] as Record<string, unknown>;
    expect(q.evidence).toBe("unconfirmed_transport");
    expect(await revocationOutcomes()).toContain("quarantined_unconfirmed");
  });

  it("Q6: the quarantine survives an MV3 WORKER RESTART and its revoke keeps retrying", async () => {
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    // Boot 1: exhaust into quarantine.
    const mock = installChromeMock();
    storage = mock.storage;
    const rf1 = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf1.fetch);
    seedPending({ attempts: REVOCATION_MAX_ATTEMPTS - 1 });
    await loadWorker();
    await settle();
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeDefined();

    // The quarantined retry is scheduled — move its persisted schedule into the
    // past, which IS "time passed" (the record is the only clock that matters).
    const persisted = quarantineRecords(storage)[0] as Record<string, unknown>;
    storage.set(KEY_REVOCATION_UNCONFIRMED, {
      ...persisted,
      nextAttemptAt: new Date(Date.now() - 1000).toISOString(),
    });
    vi.unstubAllGlobals();

    // Boot 2: an MV3 teardown loses only MEMORY — the same chrome.storage.
    const surviving = storage;
    const rf2 = revokeFetch(genericProxy401);
    (globalThis as unknown as { chrome: { storage: unknown } }).chrome.storage = {
      local: {
        get: vi.fn(async (key: string | null) =>
          key === null
            ? Object.fromEntries(surviving.entries())
            : surviving.has(key)
              ? { [key]: surviving.get(key) }
              : {},
        ),
        set: vi.fn(async (obj: Record<string, unknown>) => {
          for (const [k, v] of Object.entries(obj)) surviving.set(k, v);
        }),
        remove: vi.fn(async (key: string) => {
          surviving.delete(key);
        }),
      },
    };
    vi.stubGlobal("fetch", rf2.fetch);
    const send = await loadWorker();
    await settle();

    // The state is durable…
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.revocationUnconfirmed).toBe(true);
    // …and the revoke is still being pursued with the quarantined credential.
    expect(rf2.revokeCalls).toContain(`Bearer ${CRED.credential}`);
    expect(surviving.get(KEY_REVOCATION_UNCONFIRMED)).toBeDefined();
  });

  it("Q7: a LATER confirmation exits the quarantine honestly, with its own outcome", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_UNCONFIRMED, {
      credential: CRED.credential,
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      requestedAt: "2026-07-20T00:00:00Z",
      attempts: 48,
      evidence: "unconfirmed_generic_401",
      nextAttemptAt: "2020-01-01T00:00:00Z",
    });

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revoked");
    expect(state.state.revocationUnconfirmed).toBe(false);
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeUndefined();
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("confirmed_after_quarantine");
  });

  it("Q8 NEGATIVE: a quarantine resolution NEVER clobbers a NEWER credential's `ready` after a re-pair", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", rf.fetch);
    // The user re-paired: capability is READY on a NEW credential, while the old
    // credential's revoke is still quarantined.
    storage.set(KEY_CAPABILITY, "ready");
    storage.set(KEY_CREDENTIAL, CRED);
    storage.set(KEY_REVOCATION_UNCONFIRMED, {
      credential: "old-quarantined-credential",
      credentialId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      requestedAt: "2026-07-20T00:00:00Z",
      attempts: 48,
      evidence: "unconfirmed_generic_401",
      nextAttemptAt: "2020-01-01T00:00:00Z",
    });

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    // The NEW pairing is untouched — resolving an OLD credential's revoke must
    // never disable the extension the user just re-paired.
    expect(state.state.capability).toBe("ready");
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeUndefined();
    expect(await revocationOutcomes()).toContain("confirmed_after_quarantine");
    // The retry used the OLD, quarantined credential — never the new one.
    expect(rf.revokeCalls).toContain("Bearer old-quarantined-credential");
    expect(rf.revokeCalls).not.toContain(`Bearer ${CRED.credential}`);
  });

  it("Q9: the quarantine record is discarded at the credential's expiry — with a metric, a transition, and never silently", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_UNCONFIRMED, {
      credential: CRED.credential,
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      // Already expired: the credential cannot authenticate anything any more.
      credentialExpiresAt: "2020-01-01T00:00:00Z",
      requestedAt: "2019-12-01T00:00:00Z",
      attempts: 48,
      evidence: "unconfirmed_generic_401",
      nextAttemptAt: "2020-01-01T00:00:00Z",
    });

    const send = await loadWorker();
    await settle();

    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeUndefined();
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("quarantine_expired");
    // NEVER `revoked`: nothing was ever confirmed. It lands on "not paired".
    expect(outcomes).not.toContain("confirmed");
    expect(outcomes).not.toContain("confirmed_after_quarantine");
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("unknown");
    expect(state.state.capability).not.toBe("revoked");
    // No further request is made with a credential that cannot authenticate.
    expect(rf.revokeCalls).toEqual([]);
  });

  it("Q10: RE-PAIR is possible from quarantine, and the quarantined revoke survives it and keeps retrying", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_UNCONFIRMED, {
      credential: "old-quarantined-credential",
      credentialId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      requestedAt: "2026-07-20T00:00:00Z",
      attempts: 48,
      evidence: "unconfirmed_generic_401",
      nextAttemptAt: "2020-01-01T00:00:00Z",
    });

    const send = await loadWorker();
    const paired = await send({ kind: "pair", code: "code-123" });

    // Pairing is NOT refused — the quarantine must never lock a user out.
    if (!("state" in paired)) throw new Error(`pairing was refused: ${JSON.stringify(paired)}`);
    expect(paired.state.capability).toBe("ready");
    // …and the outstanding unconfirmed revocation is STILL VISIBLE even though
    // the capability now reads `ready` with no degradation.
    expect(paired.state.revocationUnconfirmed).toBe(true);
    expect(paired.state.degradation).toBeNull();
    // The quarantined revoke survived the re-pair and is still being retried
    // with the OLD credential.
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeDefined();
    await settle();
    expect(rf.revokeCalls).toContain("Bearer old-quarantined-credential");
  });

  // ---- the pending marker's own bounds ----

  it("Q11: an OFFLINE user pressing Pair does NOT drain the authoritative attempt budget", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => {
      throw new Error("offline");
    });
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({ attempts: 0, serverContacted: false, nextAttemptAt: "2030-01-01T00:00:00Z" });

    const send = await loadWorker();
    await settle();
    for (let i = 0; i < 6; i++) await send({ kind: "pair", code: "code-123" });

    // A FORCED retry that never reached the server is not evidence of anything,
    // so it must not consume the budget that exists to bound authoritative
    // non-answers (a reviewer probe drained 47 attempts this way).
    const marker = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown> | undefined;
    expect(marker).toBeDefined();
    expect(Number(marker?.attempts)).toBeLessThanOrEqual(1);
  });

  it("Q12: the pending marker AGES OUT into quarantine with ZERO server contact, so re-pair is never blocked forever", async () => {
    const { REVOCATION_PENDING_MAX_AGE_MS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => {
      throw new Error("offline");
    });
    vi.stubGlobal("fetch", rf.fetch);
    seedPending({
      attempts: 0,
      serverContacted: false,
      // Older than the durable age bound, and the backoff says "not due", so the
      // attempt budget can never fire.
      requestedAt: new Date(Date.now() - REVOCATION_PENDING_MAX_AGE_MS - 1000).toISOString(),
      nextAttemptAt: "2030-01-01T00:00:00Z",
    });

    const send = await loadWorker();
    await settle();

    // The marker is gone (so pairing is unblocked) and the state is the honest
    // quarantine — NEVER `revoked`: the device clock may bound a marker, but it
    // may never claim a revocation (G1).
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeDefined();
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.capability).not.toBe("revoked");
    expect(await revocationOutcomes()).not.toContain("confirmed");

    const paired = await send({ kind: "pair", code: "code-123" });
    if (!("state" in paired)) throw new Error(`pairing was refused: ${JSON.stringify(paired)}`);
    expect(paired.state.capability).toBe("ready");
  });

  // ---- fix cycle 1: the quarantine store holds MORE THAN ONE outstanding
  // revocation, and a repeat Revoke never fabricates a terminal `revoked` ----

  const CRED_A = {
    credential: "cap-cred-hex-A",
    credentialId: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
  };

  function quarantineA(over: Record<string, unknown> = {}): Record<string, unknown> {
    return {
      credential: CRED_A.credential,
      credentialId: CRED_A.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      requestedAt: "2026-07-20T00:00:00Z",
      attempts: 48,
      evidence: "unconfirmed_generic_401",
      nextAttemptAt: "2020-01-01T00:00:00Z", // already due
      ...over,
    };
  }

  // Re-arm every quarantined revoke: "time passed" is expressed through the
  // PERSISTED schedule, which is the only clock the retry consults.
  function makeQuarantineDue(): void {
    const due = new Date(Date.now() - 60_000).toISOString();
    storage.set(
      KEY_REVOCATION_UNCONFIRMED,
      quarantineRecords(storage).map((e) => ({ ...e, nextAttemptAt: due })),
    );
  }

  // B2. The quarantine store was SINGLE-SLOT: `store.set(KEY_REVOCATION_
  // UNCONFIRMED, record)` with no read-before-write. During the deploy window
  // decision (b) contemplates, the self-revoke route is unmounted so EVERY
  // revoke is unconfirmable — and the second one silently overwrote the first,
  // destroying credential A's material AND its outstanding revocation with no
  // metric, no log and no capability transition, while A's server row stayed
  // live for the rest of its TTL.
  it("B2: a SECOND unconfirmable revocation never evicts the FIRST — both stay quarantined and BOTH keep being pursued", async () => {
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    // Credential A's revoke is already quarantined.
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_UNCONFIRMED, quarantineA());

    const send = await loadWorker();
    // Re-pairing from quarantine is permitted BY DESIGN (decision (a)) …
    const paired = await send({ kind: "pair", code: "code-123" });
    if (!("state" in paired)) throw new Error(`pairing was refused: ${JSON.stringify(paired)}`);
    await settle();

    // … and credential B's revoke is unconfirmable too, so it also quarantines.
    seedPending({ attempts: REVOCATION_MAX_ATTEMPTS - 1 });
    const send2 = await loadWorker();
    await settle();

    // BOTH records survive — neither credential's outstanding revoke was lost.
    const ids = quarantineRecords(storage).map((e) => e.credentialId);
    expect(ids).toContain(CRED_A.credentialId);
    expect(ids).toContain(CRED.credentialId);
    const a = quarantineRecords(storage).find((e) => e.credentialId === CRED_A.credentialId);
    expect(a?.credential).toBe(CRED_A.credential);

    const state = await send2({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.revocationUnconfirmed).toBe(true);

    // A is STILL PURSUED after B joined the quarantine — that is the whole point
    // of keeping it: the authority still gets to kill A's server row.
    makeQuarantineDue();
    rf.revokeCalls.length = 0;
    await loadWorker();
    await settle();
    expect(rf.revokeCalls).toContain(`Bearer ${CRED_A.credential}`);
    expect(rf.revokeCalls).toContain(`Bearer ${CRED.credential}`);
    expect(await revocationOutcomes()).not.toContain("confirmed");
  });

  // B4. `revokeLocked`'s no-credential branch never consulted the quarantine. In
  // quarantine KEY_CREDENTIAL is already gone, so a repeat Revoke fired that
  // branch and overwrote the honest `revocation_unconfirmed` with terminal
  // `revoked` — the popup rendering a completed kill switch the authority never
  // confirmed, while the quarantine record was still outstanding, and telemetry
  // emitting `already_cleared` (indistinguishable from an idempotent repeat of a
  // genuinely confirmed revoke).
  it("B4: a REPEAT Revoke while quarantined never folds the quarantine into a terminal `revoked`", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    // Not due yet, so ONLY the user's repeat Revoke can drive an attempt.
    storage.set(KEY_REVOCATION_UNCONFIRMED, quarantineA({ nextAttemptAt: "2030-01-01T00:00:00Z" }));

    const send = await loadWorker();
    await settle();
    expect(rf.revokeCalls).toEqual([]);

    const resp = await send({ kind: "revoke" });
    await settle();
    if (!("state" in resp)) throw new Error("expected state");

    // The state stays HONEST — never a kill switch it did not achieve.
    expect(resp.state.capability).toBe("revocation_unconfirmed");
    expect(resp.state.capability).not.toBe("revoked");
    expect(resp.state.degradation).not.toBe("credential_revoked");
    expect(resp.state.revocationUnconfirmed).toBe(true);
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_unconfirmed");
    // The quarantine is still outstanding, and the repeat FORCED a retry of it.
    expect(quarantineRecords(storage).map((e) => e.credentialId)).toEqual([CRED_A.credentialId]);
    expect(rf.revokeCalls).toContain(`Bearer ${CRED_A.credential}`);
    // Telemetry can tell this apart from an idempotent repeat of a CONFIRMED
    // revoke — the two are not the same operational event.
    const outcomes = await revocationOutcomes();
    expect(outcomes).toContain("quarantine_repeat");
    expect(outcomes).not.toContain("already_cleared");
    expect(outcomes).not.toContain("confirmed");
    expect(await capabilityTransitions()).not.toContain("revoked");
  });

  // ---- fix cycle 2 ----

  // B5. The bounded quarantine list evicts its OLDEST entry at the cap. The
  // eviction was counted and warn-logged, but nothing DURABLE recorded that an
  // outstanding revocation had been abandoned: `PopupState.revocationUnconfirmed`
  // fell back to false and `resolveQuarantine` promoted the capability to a
  // terminal `revoked` once the REMAINING entries confirmed. The popup then
  // rendered a completed kill switch on a device where the evicted credential is
  // still live at the authority — the exact user-visible falsehood #149 is about.
  it("B5: a cap-EVICTED quarantine entry leaves a durable trace, so a later clean sweep NEVER reports a terminal `revoked`", async () => {
    const { MAX_QUARANTINED_REVOCATIONS } = await import("../lib/storage");
    const { REVOCATION_MAX_ATTEMPTS } = await import("../lib/revocation-backoff");
    storage = installChromeMock().storage;
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    let confirmed = false;
    const rf = revokeFetch(() =>
      confirmed ? new Response(null, { status: 204 }) : genericProxy401(),
    );
    vi.stubGlobal("fetch", rf.fetch);

    // The quarantine is already AT its cap: every one of these revokes is
    // outstanding and unconfirmable (the deploy window decision (b) contemplates).
    const seeded = Array.from({ length: MAX_QUARANTINED_REVOCATIONS }, (_, i) =>
      quarantineA({
        credential: `cap-cred-hex-${i}`,
        credentialId: `qqqqqqqq-0000-0000-0000-00000000000${i}`,
        nextAttemptAt: "2030-01-01T00:00:00Z", // only the 9th revoke moves anything
      }),
    );
    storage.set(KEY_REVOCATION_UNCONFIRMED, seeded);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    // …and a NINTH revoke exhausts its budget, so it quarantines too.
    seedPending({ attempts: REVOCATION_MAX_ATTEMPTS - 1 });

    const send = await loadWorker();
    await settle();

    // The oldest entry was evicted, observably.
    const ids = quarantineRecords(storage).map((e) => e.credentialId);
    expect(ids).toHaveLength(MAX_QUARANTINED_REVOCATIONS);
    expect(ids).not.toContain("qqqqqqqq-0000-0000-0000-000000000000");
    expect(ids).toContain(CRED.credentialId);
    expect(await revocationOutcomes()).toContain("quarantine_evicted");
    expect(
      warn.mock.calls.some((c) =>
        String(c[0]).includes("credential_revocation_quarantine_evicted"),
      ),
    ).toBe(true);
    // The eviction left a DURABLE trace, so the abandoned revocation stays
    // VISIBLE even once every remaining entry is resolved.
    const evicted = await send({ kind: "getState" });
    if (!("state" in evicted)) throw new Error("expected state");
    expect(evicted.state.revocationUnconfirmed).toBe(true);

    // The gateway now recovers and confirms every entry it is still given.
    confirmed = true;
    makeQuarantineDue();
    rf.revokeCalls.length = 0;
    const respawned = await loadWorker();
    await settle();

    // The evicted credential was never presented to the authority again…
    expect(rf.revokeCalls).not.toContain("Bearer cap-cred-hex-0");
    expect(quarantineRecords(storage)).toEqual([]);
    // …so the kill switch is NOT complete, and the popup must not claim it is.
    const state = await respawned({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).not.toBe("revoked");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.degradation).not.toBe("credential_revoked");
    expect(state.state.revocationUnconfirmed).toBe(true);
    expect(await capabilityTransitions()).not.toContain("revoked");
    // The WITHHELD terminal is emitted, not merely reflected in the state: a
    // fallback that engages without an emitted signal is always a bug
    // (CLAUDE.md), and `terminal_withheld_abandoned` is the only thing telling an
    // operator this device holds a revocation it can no longer pursue.
    expect(await revocationOutcomes()).toContain("terminal_withheld_abandoned");
    expect(
      warn.mock.calls.some((c) => String(c[0]).includes("credential_revocation_terminal_withheld")),
    ).toBe(true);
  });

  // B6 (upheld). readQuarantine dropped entries with no `credentialId`, and the
  // drop was persisted by the next writeQuarantine — a credential discard with no
  // metric and no log, the one remaining unaudited discard in the quarantine store.
  it("B6: a malformed quarantine entry is never dropped SILENTLY — the discard is counted and logged", async () => {
    storage = installChromeMock().storage;
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    const malformed = quarantineA();
    delete malformed.credentialId;
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_UNCONFIRMED, [malformed, quarantineA()]);

    await loadWorker();
    await settle();

    expect(await revocationOutcomes()).toContain("quarantine_malformed_dropped");
    expect(
      warn.mock.calls.some((c) =>
        String(c[0]).includes("credential_revocation_quarantine_malformed"),
      ),
    ).toBe(true);
  });

  // ---- fix cycle 3 ----

  // B7. `revokeLocked`'s no-credential / no-quarantine branch was the ONE
  // fail-closed branch in the file without the never-clobber guard its three
  // siblings carry, and the one terminal that never consulted the durable
  // abandoned flag. Seeded with EXACTLY the state B5 leaves behind — flag set,
  // quarantine swept empty, capability withheld at `revocation_unconfirmed` — a
  // repeat Revoke promoted straight to terminal `revoked`, so the popup rendered
  // a COMPLETED kill switch on a device whose cap-evicted credential may still
  // be live at the authority, and `resolveQuarantine`'s withholding became
  // permanently inert (it early-returns unless the capability still reads
  // `revocation_unconfirmed`).
  it("B7: a repeat Revoke never fabricates a terminal `revoked` while a cap-EVICTED revocation is still abandoned", async () => {
    storage = installChromeMock().storage;
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const rf = revokeFetch(() => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", rf.fetch);
    // The post-B5 resting state: nothing left to retry, but a revocation the
    // authority never confirmed was abandoned at the cap.
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_ABANDONED, true);

    const send = await loadWorker();
    await settle();
    const resp = await send({ kind: "revoke" });
    await settle();
    if (!("state" in resp)) throw new Error("expected state");

    // The kill switch is NOT complete and the popup must not claim it is.
    expect(resp.state.capability).not.toBe("revoked");
    expect(resp.state.capability).toBe("revocation_unconfirmed");
    expect(resp.state.degradation).not.toBe("credential_revoked");
    expect(resp.state.revocationUnconfirmed).toBe(true);
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_unconfirmed");
    expect(await capabilityTransitions()).not.toContain("revoked");
    // The withholding is OBSERVED — a fallback engaging silently is always a bug
    // (CLAUDE.md), and this outcome is the only signal an operator gets.
    expect(await revocationOutcomes()).toContain("terminal_withheld_abandoned");
    expect(
      warn.mock.calls.some((c) => String(c[0]).includes("credential_revocation_terminal_withheld")),
    ).toBe(true);
  });

  // B7 NEGATIVE. Withholding a terminal must never fail OPEN. This branch removes
  // the pending marker on its way through, and the marker is what
  // `getCapability` uses to override a stale stored `ready` — so a withheld
  // terminal that left the capability untouched would put capture back ON with
  // no credential at all. The withheld state is `revocation_unconfirmed`: more
  // restrictive than what it replaced, never less.
  it("B7 NEGATIVE: withholding the terminal never leaves a CAPTURING capability behind", async () => {
    storage = installChromeMock().storage;
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const rf = revokeFetch(() => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", rf.fetch);
    // The composed teardown the F6/F7 tests establish: the credential is gone but
    // the capability write was lost, so the STORED capability is still `ready`.
    storage.set(KEY_CAPABILITY, "ready");
    storage.set(KEY_REVOCATION_ABANDONED, true);

    const send = await loadWorker();
    await settle();
    const resp = await send({ kind: "revoke" });
    await settle();
    if (!("state" in resp)) throw new Error("expected state");

    expect(resp.state.capability).not.toBe("ready");
    expect(resp.state.capability).not.toBe("revoked");
    expect(resp.state.capability).toBe("revocation_unconfirmed");
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_unconfirmed");
    expect(await revocationOutcomes()).toContain("terminal_withheld_abandoned");
    expect(await capabilityTransitions()).not.toContain("revoked");
  });

  // B7 NEVER-PROMOTE. The abandoned-flag half of the guard (B7 / B7 NEGATIVE /
  // B7 TIMER) is only ONE of its two parts. The other is the never-promote check
  // the three sibling fail-closed branches carry: `unknown`, `disabled` and
  // `revocation_unconfirmed` are left exactly as they are, because only a state
  // that was capturing (`ready`) or actively revoking (`revocation_pending`) has
  // a revoke to settle. Dropping that check — reaching the terminal
  // unconditionally once nothing is abandoned — left the whole suite green while
  // a second Revoke press promoted a resting `revocation_unconfirmed` straight
  // to terminal `revoked`, rendering the `credential_revoked` degradation for a
  // kill switch no authority ever confirmed. That resting state is
  // production-reachable with NO abandoned flag involved: `failClosedDurably`
  // writes it whenever a Revoke's storage mirror fails with no credential left
  // to retry with (`local_storage_error_recovered`).
  it.each(["revocation_unconfirmed", "unknown", "disabled"] as const)(
    "B7 NEVER-PROMOTE: a repeat Revoke from a resting `%s` is `already_cleared` — it never promotes to terminal `revoked`",
    async (seeded) => {
      storage = installChromeMock().storage;
      vi.spyOn(console, "warn").mockImplementation(() => {});
      const rf = revokeFetch(() => new Response(null, { status: 204 }));
      vi.stubGlobal("fetch", rf.fetch);
      // Nothing abandoned, nothing quarantined, no credential, no marker: the
      // ONLY thing standing between this branch and a fabricated terminal is the
      // never-promote check.
      storage.set(KEY_CAPABILITY, seeded);

      const send = await loadWorker();
      await settle();
      const resp = await send({ kind: "revoke" });
      await settle();
      if (!("state" in resp)) throw new Error("expected state");

      // The resting state is left EXACTLY as it was — more restrictive is
      // allowed, promotion to a completed kill switch is not.
      expect(resp.state.capability).toBe(seeded);
      expect(resp.state.capability).not.toBe("revoked");
      expect(storage.get(KEY_CAPABILITY)).toBe(seeded);
      expect(storage.get(KEY_CAPABILITY)).not.toBe("revoked");
      // The popup must never claim the kill switch completed.
      expect(resp.state.degradation).not.toBe("credential_revoked");
      expect(await capabilityTransitions()).not.toContain("revoked");
      // The repeat is still the idempotent, bounded outcome — withholding the
      // terminal does not make the event unobservable.
      const outcomes = await revocationOutcomes();
      expect(outcomes).toContain("already_cleared");
      expect(outcomes).not.toContain("orphaned");
      expect(outcomes).not.toContain("confirmed");
    },
  );

  // B7 TIMER. The user-driven repeat Revoke is not the only route to that
  // terminal: the boot-time pending retry reaches the identical situation (a
  // marker whose credential material is gone) through `demoteToRevoked`, and it
  // reached terminal `revoked` on a BOOT TICK with the abandoned flag set —
  // capability `revoked`, degradation `credential_revoked`, no user action at
  // all. Every terminal reached by INFERENCE withholds while a cap-evicted
  // revocation is outstanding; only a positively-proven one does not.
  it("B7 TIMER: the BOOT-tick orphan resolution also withholds terminal `revoked` while a revocation is abandoned", async () => {
    storage = installChromeMock().storage;
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    // A durable marker whose credential is gone, over a stored `ready` that the
    // lost capability write left behind (the F6/F7 composed teardown).
    storage.set(KEY_CAPABILITY, "ready");
    storage.set(KEY_REVOCATION_ABANDONED, true);
    storage.set(KEY_REVOCATION_PENDING, {
      requestedAt: new Date().toISOString(),
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      attempts: 1,
      serverContacted: true,
    });

    const send = await loadWorker();
    await settle();

    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).not.toBe("revoked");
    expect(state.state.capability).not.toBe("ready");
    expect(state.state.degradation).not.toBe("credential_revoked");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_unconfirmed");
    expect(await capabilityTransitions()).not.toContain("revoked");
    expect(await revocationOutcomes()).toContain("terminal_withheld_abandoned");
    // The STRUCTURED LOG too, as B5 / B7 / B8 assert: the counter alone left the
    // warn deletable with the suite still green, and a fallback that engages
    // without an emitted signal is always a bug (CLAUDE.md).
    expect(
      warn.mock.calls.some((c) => String(c[0]).includes("credential_revocation_terminal_withheld")),
    ).toBe(true);
  });

  // B8. The expiry terminal's abandoned-withholding branch (the other half of the
  // fix B5 pins) had NO test: deleting it and restoring the unconditional
  // `setCapability("unknown")` left the whole suite green, on the path that
  // decides how a possibly-live credential's state resolves. This mirrors B5 but
  // drives the EXPIRY terminal instead of the confirmation terminal.
  it("B8: the EXPIRY terminal also withholds while a cap-EVICTED revocation is abandoned — never `unknown`", async () => {
    storage = installChromeMock().storage;
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_ABANDONED, true);
    // The last remaining entry ages out at its authoritative expiry: its material
    // is discarded, but that is not the authority confirming anything.
    storage.set(KEY_REVOCATION_UNCONFIRMED, [
      quarantineA({ credentialExpiresAt: "2020-01-01T00:00:00Z" }),
    ]);

    const send = await loadWorker();
    await settle();

    // The expired entry was discarded without ever being presented again…
    expect(rf.revokeCalls).toEqual([]);
    expect(quarantineRecords(storage)).toEqual([]);
    expect(await revocationOutcomes()).toContain("quarantine_expired");
    // …and the sweep does NOT resolve to `unknown`: the evicted revocation is
    // still outstanding at the authority.
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.capability).not.toBe("unknown");
    expect(state.state.capability).not.toBe("revoked");
    expect(state.state.capability).toBe("revocation_unconfirmed");
    expect(state.state.revocationUnconfirmed).toBe(true);
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_unconfirmed");
    expect(await capabilityTransitions()).not.toContain("unknown");
    // Observed, not silent.
    expect(await revocationOutcomes()).toContain("terminal_withheld_abandoned");
    expect(
      warn.mock.calls.some((c) => String(c[0]).includes("credential_revocation_terminal_withheld")),
    ).toBe(true);
  });

  // B9. `finalizeRevocation` is the ONE terminal with POSITIVE PROOF for the
  // credential in hand (204, or the authority's own CAPTURE_CREDENTIAL_INVALID
  // verdict), so it KEEPS the terminal `revoked` — withholding there would deny a
  // confirmation the authority actually gave, and would leave the capability on a
  // pending state for a credential that is provably dead. What it must never do
  // is make an unrelated ABANDONED revocation invisible: the popup's outstanding-
  // revocation paragraph has to render alongside the completed kill switch.
  it("B9: a CONFIRMED revocation stays terminal `revoked`, but an abandoned revocation stays VISIBLE alongside it", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(() => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_REVOCATION_ABANDONED, true);

    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    const resp = await send({ kind: "revoke" });
    await settle();
    if (!("state" in resp)) throw new Error("expected state");

    expect(resp.state.capability).toBe("revoked");
    expect(await revocationOutcomes()).toContain("confirmed");
    // …and the abandoned revocation is NOT hidden by that completion.
    expect(resp.state.revocationUnconfirmed).toBe(true);
    const state = await send({ kind: "getState" });
    if (!("state" in state)) throw new Error("expected state");
    expect(state.state.revocationUnconfirmed).toBe(true);
  });

  // B10 (upheld). `readQuarantine` pruned malformed entries in memory and relied
  // on the NEXT writeQuarantine to persist the drop — true only for a MIXED list
  // (all B6 covers). When EVERY entry is malformed the pruned list was never
  // written, so credential material sat in storage forever; and because
  // `popupState` reads the quarantine, the drop was counted and warn-logged on
  // EVERY getState poll — a counter measuring reads, not discards.
  it("B10: an ALL-malformed quarantine list is persisted-pruned once, and the discard is counted once, not per read", async () => {
    storage = installChromeMock().storage;
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    const malformed = quarantineA();
    delete malformed.credentialId;
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");
    storage.set(KEY_REVOCATION_UNCONFIRMED, [malformed]);

    const send = await loadWorker();
    await settle();

    // The credential material is GONE from storage — not retained forever.
    expect(quarantineRecords(storage)).toEqual([]);
    expect(storage.get(KEY_REVOCATION_UNCONFIRMED)).toBeUndefined();
    expect(await revocationOutcomes()).toContain("quarantine_malformed_dropped");

    // …and the counter measures DISCARDS, not reads: polling the popup three
    // times does not re-count a discard that already happened.
    const after = await revocationOutcomeCount("quarantine_malformed_dropped");
    for (let i = 0; i < 3; i++) await send({ kind: "getState" });
    expect(await revocationOutcomeCount("quarantine_malformed_dropped")).toBe(after);
    expect(after).toBe(1);
  });

  // B11 (upheld). `handleSetEnabled` guarded only `revocation_pending`, so a
  // `revocation_unconfirmed` capability plus a stored credential let the toggle
  // write a plain `disabled` — masking the "could not confirm" state as an
  // ordinary user disable and losing the visible distinctness the state exists
  // for. Fail-closed either way, but the state must stay honest.
  it("B11: the capture toggle never masks a `revocation_unconfirmed` capability as a plain disable", async () => {
    storage = installChromeMock().storage;
    const rf = revokeFetch(genericProxy401);
    vi.stubGlobal("fetch", rf.fetch);
    storage.set(KEY_CREDENTIAL, CRED);
    storage.set(KEY_CAPABILITY, "revocation_unconfirmed");

    const send = await loadWorker();
    await settle();
    const resp = await send({ kind: "setEnabled", enabled: false });
    if (!("state" in resp)) throw new Error("expected state");

    expect(resp.state.capability).toBe("revocation_unconfirmed");
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_unconfirmed");
    expect(storage.get(KEY_CAPABILITY)).not.toBe("disabled");
    // …and it is still not re-enablable.
    const back = await send({ kind: "setEnabled", enabled: true });
    if (!("state" in back)) throw new Error("expected state");
    expect(back.state.capability).toBe("revocation_unconfirmed");
  });
});

// Issue #149, fix 3 — the two carried-over HIGH blockers.
describe("service worker — #149 F1/F2: the kill switch never fails OPEN", () => {
  const KEY_REVOCATION_PENDING = "revocationPending";
  const product = parsedProduct();

  const settle = async () => {
    for (let i = 0; i < 30; i++) await new Promise((r) => setTimeout(r, 0));
  };

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  // F1. handleRevoke's durable writes were unguarded. chrome.storage.local.set
  // CAN reject (QUOTA_BYTES) — handlePair was guarded for exactly this, the
  // USER-INITIATED kill switch was left bare. The rejection escaped
  // `handle().then(sendResponse)`, so the popup got NO response at all, the
  // capability stayed `ready`, zero telemetry was emitted, and the next
  // scheduled refresh happily re-synced owned targets with the very credential
  // being revoked. That is failing OPEN on the one path that must never do so.
  it("F1: a storage rejection during Revoke still responds, still disables capture, and is observable", async () => {
    const mock = installChromeMock();
    const storage = mock.storage;
    const gatewayCalls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string) => {
        const url = String(input);
        if (url.includes("/ext/pairing/claim")) {
          return new Response(JSON.stringify(CRED), { status: 200 });
        }
        gatewayCalls.push(url);
        if (url.includes("/ext/owned-targets")) {
          return new Response(JSON.stringify({ items: [ownedTargetRow(product)] }), {
            status: 200,
          });
        }
        return new Response(null, { status: 202 });
      }),
    );
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });

    // Storage breaks exactly when the revoke needs to record itself.
    const chromeMock = (
      globalThis as unknown as { chrome: { storage: { local: { set: ReturnType<typeof vi.fn> } } } }
    ).chrome;
    chromeMock.storage.local.set = vi.fn(async () => {
      throw new Error("QUOTA_BYTES quota exceeded");
    });
    gatewayCalls.length = 0;

    // 1. The message handler ALWAYS responds — the popup is never left hanging.
    const resp = await send({ kind: "revoke" });
    expect(resp).toBeDefined();
    expect("state" in resp).toBe(true);
    if (!("state" in resp)) throw new Error("expected state");

    // 2. Capture is disabled even though the durable write failed. Storage is
    //    exactly what is broken, so an in-memory gate is the only honest option.
    expect(resp.state.capability).not.toBe("ready");
    const after = await send({ kind: "getState" });
    if (!("state" in after)) throw new Error("expected state");
    expect(after.state.capability).not.toBe("ready");

    // 3. It is OBSERVABLE — never a swallowed exception.
    const { snapshotMetrics } = await import("../lib/observability");
    const outcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
    expect(outcomes.length).toBeGreaterThan(0);
    expect(outcomes).not.toContain("confirmed");

    // 4. Nothing keeps using the credential being revoked.
    await send({ kind: "capture", product });
    await settle();
    expect(gatewayCalls.filter((u) => u.includes("/observation/capture"))).toEqual([]);
    // 5. The DURABLE snapshot is fail-closed too, by VALUE: `set` is broken, so
    //    the fallback exhausts the options a broken-quota store still has —
    //    `remove` FREES quota — leaving no credential a respawn could capture
    //    with and no stored `ready` for it to read back.
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_CAPABILITY)).not.toBe("ready");
  });

  // B1 (fix cycle 1). The F1 gate was MEMORY-ONLY: `localCaptureLock` is
  // module-level state that dies with the MV3 worker. After a Revoke whose
  // durable writes rejected, the surviving snapshot was
  // {credential, capability:"ready"} with NO marker — so MV3's idle teardown
  // plus the next respawn read `ready`, re-synced owned targets and UPLOADED
  // with the credential the user had just asked to revoke, and no durable marker
  // existed for `retryPendingRevocation` to reconcile from either. The kill
  // switch has to survive the worker, not merely the worker's lifetime.
  it("B1: a Revoke whose durable writes fail still fails CLOSED after a WORKER RESTART — the respawned worker never captures with the revoked-by-request credential", async () => {
    const mock = installChromeMock();
    const storage = mock.storage;
    const gatewayCalls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string) => {
        const url = String(input);
        if (url.includes("/ext/pairing/claim")) {
          return new Response(JSON.stringify(CRED), { status: 200 });
        }
        gatewayCalls.push(url);
        if (url.includes("/ext/owned-targets")) {
          return new Response(JSON.stringify({ items: [ownedTargetRow(product)] }), {
            status: 200,
          });
        }
        return new Response(null, { status: 202 });
      }),
    );
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await settle();

    // Storage breaks exactly when the revoke needs to record itself. `remove`
    // keeps working, as it does in a real QUOTA_BYTES failure — removing frees
    // quota rather than consuming it.
    const chromeMock = (
      globalThis as unknown as { chrome: { storage: { local: { set: ReturnType<typeof vi.fn> } } } }
    ).chrome;
    const brokenSet = vi.fn(async () => {
      throw new Error("QUOTA_BYTES quota exceeded");
    });
    chromeMock.storage.local.set = brokenSet;
    await send({ kind: "revoke" });
    await settle();

    // MV3 tears the worker down on idle. Memory (including localCaptureLock) is
    // gone; chrome.storage.local survives, and quota has since been freed.
    const surviving = storage;
    chromeMock.storage.local.set = vi.fn(async (obj: Record<string, unknown>) => {
      for (const [k, v] of Object.entries(obj)) surviving.set(k, v);
    });
    gatewayCalls.length = 0;
    const respawned = await loadWorker();
    await settle();
    await respawned({ kind: "capture", product });
    await settle();

    // The respawned worker never uploads with the revoked-by-request credential.
    expect(gatewayCalls.filter((u) => u.includes("/observation/capture"))).toEqual([]);
    // …and its capability VALUE is the honest fail-closed one — asserting it is
    // merely DEFINED would pass over exactly the bug (`ready` is defined).
    const after = await respawned({ kind: "getState" });
    if (!("state" in after)) throw new Error("expected state");
    expect(after.state.capability).not.toBe("ready");
    expect(after.state.capability).toBe("unknown");
    expect(surviving.get(KEY_CREDENTIAL)).toBeUndefined();
  });

  // B1-partial (fix cycle 2). The cycle-1 last-resort discard fired whenever the
  // CAPABILITY MIRROR write failed — even when a durable pending marker AND the
  // credential both survived. That state needs no repair: getCapability treats
  // the marker as authoritative over a stale stored `ready`, so it is already
  // fail-closed, and the marker + credential are the ONLY material the SERVER
  // revoke can still be made with. Discarding them destroyed a revoke that was
  // fully alive at the authority — one EXT-009 defeat traded for another.
  it("B1-partial: a Revoke whose CAPABILITY mirror write fails but whose durable marker SURVIVES is never discarded — a later boot still completes the revoke at the authority", async () => {
    const mock = installChromeMock();
    const storage = mock.storage;
    const revokeCalls: string[] = [];
    let gatewayUp = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/ext/pairing/claim")) {
          return new Response(JSON.stringify(CRED), { status: 200 });
        }
        if (url.includes("/ext/pairing/self-revoke")) {
          if (!gatewayUp) throw new Error("gateway unreachable");
          revokeCalls.push(
            (init?.headers as Record<string, string> | undefined)?.authorization ?? "",
          );
          return new Response(null, { status: 204 });
        }
        if (url.includes("/ext/owned-targets")) {
          return new Response(JSON.stringify({ items: [] }), { status: 200 });
        }
        return new Response(null, { status: 202 });
      }),
    );
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await settle();

    // Revoke #1 lands durably and stays pending — the gateway is unreachable.
    await send({ kind: "revoke" });
    await settle();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();

    // Storage now rejects every `set`, so the capability MIRROR write is what
    // fails — and the user presses Revoke a SECOND time, because the popup still
    // says "awaiting confirmation".
    const chromeMock = (
      globalThis as unknown as { chrome: { storage: { local: { set: ReturnType<typeof vi.fn> } } } }
    ).chrome;
    chromeMock.storage.local.set = vi.fn(async () => {
      throw new Error("QUOTA_BYTES quota exceeded");
    });
    await send({ kind: "revoke" });
    await settle();

    // NOTHING was destroyed: the marker already holds capture off (it overrides a
    // stale stored `ready`), and it plus the credential are what a retry needs.
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeDefined();
    const { snapshotMetrics } = await import("../lib/observability");
    const outcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
    expect(outcomes).toContain("local_storage_error_marker_retained");
    expect(outcomes).not.toContain("local_storage_error_discarded");
    // Capture is off regardless — the marker override, not the discard, is what
    // fails this closed.
    const during = await send({ kind: "getState" });
    if (!("state" in during)) throw new Error("expected state");
    expect(during.state.capability).not.toBe("ready");

    // COUNTERFACTUAL: the revoke was still completable. Storage recovers, the
    // gateway comes back, and the respawned worker reaches the AUTHORITY.
    chromeMock.storage.local.set = vi.fn(async (obj: Record<string, unknown>) => {
      for (const [k, v] of Object.entries(obj)) storage.set(k, v);
    });
    gatewayUp = true;
    storage.set(KEY_REVOCATION_PENDING, {
      ...(storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>),
      nextAttemptAt: "2020-01-01T00:00:00Z", // due
    });
    const respawned = await loadWorker();
    await settle();

    expect(revokeCalls).toContain(`Bearer ${CRED.credential}`);
    const after = await respawned({ kind: "getState" });
    if (!("state" in after)) throw new Error("expected state");
    expect(after.state.capability).toBe("revoked");
    expect(storage.get(KEY_CREDENTIAL)).toBeUndefined();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined();
  });

  // Upheld (cycle 2). The `local_storage_error_recovered` MIDDLE path — the small
  // fail-closed writes landing after the shed — shipped with no test at all,
  // including the `if (!existing)` guard that keeps a repeatedly-failing write
  // from resetting the marker's AGE bound (the bound is what stops a marker,
  // which BLOCKS re-pairing, from outliving a device that never reaches the
  // authority). The shed itself must be observable too (CLAUDE.md: load shedding
  // is explicit and prioritized, observed, never silent).
  it("recovered middle path: a retried fail-closed write keeps the marker and never resets its age bound, and the telemetry shed is observed", async () => {
    const mock = installChromeMock();
    const storage = mock.storage;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string) => {
        const url = String(input);
        if (url.includes("/ext/pairing/claim")) {
          return new Response(JSON.stringify(CRED), { status: 200 });
        }
        if (url.includes("/ext/pairing/self-revoke")) throw new Error("gateway unreachable");
        if (url.includes("/ext/owned-targets")) {
          return new Response(JSON.stringify({ items: [] }), { status: 200 });
        }
        return new Response(null, { status: 202 });
      }),
    );
    const send = await loadWorker();
    await send({ kind: "pair", code: "code-123" });
    await settle();
    await send({ kind: "revoke" });
    await settle();
    const first = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    expect(first).toBeDefined();
    const requestedAt = String(first.requestedAt);

    // The NEXT durable write rejects exactly once; the retried, smaller writes
    // inside the fallback land.
    const chromeMock = (
      globalThis as unknown as { chrome: { storage: { local: { set: ReturnType<typeof vi.fn> } } } }
    ).chrome;
    let failed = false;
    chromeMock.storage.local.set = vi.fn(async (obj: Record<string, unknown>) => {
      if (!failed) {
        failed = true;
        throw new Error("QUOTA_BYTES quota exceeded");
      }
      for (const [k, v] of Object.entries(obj)) storage.set(k, v);
    });

    await send({ kind: "revoke" });
    await settle();

    // The revoke stays durably retryable, and its AGE BOUND is NOT extended by a
    // failing write.
    const marker = storage.get(KEY_REVOCATION_PENDING) as Record<string, unknown>;
    expect(marker).toBeDefined();
    expect(marker.requestedAt).toBe(requestedAt);
    expect(storage.get(KEY_CREDENTIAL)).toBeDefined();
    expect(storage.get(KEY_CAPABILITY)).toBe("revocation_pending");
    const { snapshotMetrics } = await import("../lib/observability");
    const outcomes = snapshotMetrics()
      .filter((s) => s.name === "credential_revocation")
      .map((s) => String(s.labels.outcome));
    expect(outcomes).toContain("local_storage_error_recovered");
    expect(outcomes).not.toContain("local_storage_error_discarded");
    // The advisory-telemetry shed is OBSERVED, not silent.
    expect(outcomes).toContain("telemetry_shed");
  });

  // Upheld (cycle 2). failClosedDurably's no-credential branch wrote
  // `revocation_unconfirmed` UNCONDITIONALLY, unlike its three siblings, so it
  // could rewrite a terminal `revoked` (a CONFIRMED kill switch) back into "could
  // not confirm". The override is stated to be one-way — more restrictive only.
  it("the no-credential fallback never rewrites a CONFIRMED `revoked` into `revocation_unconfirmed`", async () => {
    const mock = installChromeMock();
    const storage = mock.storage;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 202 })),
    );
    // A completed, confirmed revocation: no credential, no marker, no quarantine.
    storage.set(KEY_CAPABILITY, "revoked");
    const send = await loadWorker();

    // `remove` now rejects, so the repeat Revoke's own durable write fails and
    // the fallback runs with NO credential present.
    const chromeMock = (
      globalThis as unknown as {
        chrome: { storage: { local: { remove: ReturnType<typeof vi.fn> } } };
      }
    ).chrome;
    chromeMock.storage.local.remove = vi.fn(async () => {
      throw new Error("QUOTA_BYTES quota exceeded");
    });

    const resp = await send({ kind: "revoke" });
    await settle();

    if (!("state" in resp)) throw new Error("expected state");
    expect(storage.get(KEY_CAPABILITY)).toBe("revoked");
    expect(resp.state.capability).toBe("revoked");
  });

  // F2. `demoteToRevoked` did not mirror the #253 owned-target teardown that
  // handleRevoke and tearDownCredential both perform, so an ORPHAN resolution
  // ended a revoke while the in-memory Confirmed-owned-target projection
  // survived — account A's target could then be uploaded on a request
  // authenticated with account B's credential (identity quarantine, §4.6).
  it("F2: an ORPHAN resolution tears the owned-target projection down, like every other revoke path", async () => {
    const mock = installChromeMock();
    const storage = mock.storage;
    const uploads: string[] = [];
    const CRED_B = {
      credential: "cap-cred-hex-B",
      credentialId: "55555555-5555-5555-5555-555555555555",
      marketplaceAccountId: "22222222-2222-2222-2222-222222222222",
      expiresAt: "2026-08-01T00:00:00Z",
    };
    // Account A pairs first and owns the product; account B owns NOTHING. So an
    // upload of A's target on B's credential can ONLY come from A's in-memory
    // projection surviving a revoke — the identity-quarantine breach under test.
    let claim: Record<string, unknown> = CRED;
    let ownedRows: unknown[] = [ownedTargetRow(product)];
    // A gate on the owned-target read, so the test can hold B's sync OPEN and
    // capture inside the window where the capability is already `ready` but the
    // new account's projection has not landed yet. That window is exactly why
    // every revoke path clears the index rather than relying on the next sync.
    let releaseOwned: (() => void) | null = null;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: string, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/ext/pairing/claim")) {
          return new Response(JSON.stringify(claim), { status: 200 });
        }
        if (url.includes("/ext/owned-targets")) {
          if (releaseOwned) {
            await new Promise<void>((r) => {
              releaseOwned = r;
            });
          }
          return new Response(JSON.stringify({ items: ownedRows }), { status: 200 });
        }
        if (url.includes("/observation/capture")) {
          uploads.push((init?.headers as Record<string, string> | undefined)?.authorization ?? "");
          return new Response(null, { status: 202 });
        }
        return new Response(null, { status: 202 });
      }),
    );

    const send = await loadWorker();
    await send({ kind: "pair", code: "code-A" });
    await settle();
    // Account A really is capture-ready with a populated projection.
    await send({ kind: "capture", product });
    await settle();
    expect(uploads).toEqual([`Bearer ${CRED.credential}`]);
    uploads.length = 0;

    // Force the ORPHAN terminal: a durable pending marker whose credential
    // material is gone, so no retry can ever succeed and the revoke resolves
    // LOCALLY (demoteToRevoked) — the path that did not mirror the teardown.
    storage.delete(KEY_CREDENTIAL);
    storage.set(KEY_CAPABILITY, "revocation_pending");
    storage.set(KEY_REVOCATION_PENDING, {
      requestedAt: "2026-07-20T00:00:00Z",
      credentialId: CRED.credentialId,
      marketplaceAccountId: CRED.marketplaceAccountId,
      credentialExpiresAt: CRED.expiresAt,
      attempts: 1,
      serverContacted: true,
      nextAttemptAt: "2020-01-01T00:00:00Z",
    });
    const alarm = (
      globalThis as unknown as {
        chrome: { alarms: { onAlarm: { addListener: ReturnType<typeof vi.fn> } } };
      }
    ).chrome.alarms.onAlarm.addListener.mock.calls[0]?.[0] as (a: { name: string }) => void;
    alarm({ name: "queue-flush" });
    await settle();
    expect(storage.get(KEY_REVOCATION_PENDING)).toBeUndefined(); // orphan resolved

    // The user now re-pairs as account B, and captures A's product INSIDE the
    // window where B's owned-target sync has not completed.
    claim = CRED_B;
    ownedRows = [];
    releaseOwned = () => {};
    const pairing = send({ kind: "pair", code: "code-B" });
    await settle();
    await send({ kind: "capture", product });
    await settle();
    releaseOwned?.();
    releaseOwned = null;
    await pairing;
    await settle();

    // Account B owns nothing. A surviving account-A projection shows up here as
    // A's target uploaded on B's credential (§4.6 identity quarantine).
    expect(uploads).toEqual([]);
  });
});
