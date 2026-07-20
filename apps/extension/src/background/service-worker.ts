import { type Capability, degradationReason } from "../lib/capability";
import { GatewayClient } from "../lib/gateway";
import { buildHistorySeries } from "../lib/history";
import { createHistoryReadGateway } from "../lib/history-read";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import { gauge, incr, log, snapshotMetrics } from "../lib/observability";
import { deriveOverlayView } from "../lib/overlay-data";
import { createOverlayReadGateway } from "../lib/overlay-read";
import { OwnedTargetIndex } from "../lib/owned-targets";
import { prepareCapture } from "../lib/pipeline";
import { UploadQueue } from "../lib/queue";
import { pendingAllocationGateway, runScheduledCycle } from "../lib/schedule";
import { initDevErrorReporting } from "../lib/spotlight";
import {
  chromeLocalStore,
  KEY_CAPABILITY,
  KEY_CREDENTIAL,
  KEY_LAST_UPLOAD,
  type PopupState,
  sanitizeCredential,
} from "../lib/storage";
import {
  TelemetryOutbox,
  type TelemetryTransport,
  unavailableTelemetryTransport,
} from "../lib/telemetry-outbox";
import type { PairingCredential, ParsedProduct } from "../lib/types";
import { createWatchlistGateway } from "../lib/watchlist";

// The MV3 service worker owns pairing, queueing, and delivery discipline (docs/09).
// It holds ONLY a capture credential (EXT-001), gates every capture on capability
// + Confirmed ownership, and flushes the offline queue with bounded backoff.
// Alarms are scheduling HINTS for queue flush — never autonomous crawling.

const GATEWAY_BASE_URL = import.meta.env.VITE_GATEWAY_BASE_URL ?? "http://localhost:8080";
const FLUSH_ALARM = "queue-flush";
const SCHEDULE_ALARM = "scheduled-refresh";
const KEY_SCHEDULE_ENABLED = "scheduleEnabled";

const store = chromeLocalStore();
const queue = new UploadQueue(store);
// Durable operational-telemetry outbox (issue #162): a bounded, allow-listed
// metric snapshot in chrome.storage that survives MV3 worker restarts. The
// transport is the SINGLE export seam; while no capture-credential-scoped
// telemetry endpoint exists in the gateway contract (BLOCKED-on-endpoint), the
// default keeps batches durably pending within the cap and NEVER blocks capture.
const telemetry = new TelemetryOutbox(store);
const telemetryTransport: TelemetryTransport = unavailableTelemetryTransport;
const gateway = new GatewayClient(GATEWAY_BASE_URL);
const watchlistGateway = createWatchlistGateway(GATEWAY_BASE_URL);
const overlayReadGateway = createOverlayReadGateway();
const historyReadGateway = createHistoryReadGateway();
// Confirmed owned targets: server-authoritative, starts EMPTY (fail closed —
// EXT-004). Populated by syncOwnedTargets() from the credential-scoped read
// (GET /ext/owned-targets, #145) on worker start, after pairing, and periodically.
const ownedTargets = new OwnedTargetIndex();

// Sync/lifecycle generation (issue #253). A monotonic counter bumped on EVERY
// newer sync AND on every revoke/credential replacement. An owned-target sync
// result — success OR failure — is applied ONLY if its generation is still the
// newest when it completes; an older in-flight request (started under credential
// A, completing after A was revoked or replaced by B) is stale and ignored. This
// is the ordering half of the guard; credentialFingerprint is the identity half.
// Declared before the module-init syncOwnedTargets() call below so it is not in
// the temporal dead zone when the cold-start sync runs.
let syncGeneration = 0;

void initDevErrorReporting("service-worker");

// On service-worker start (cold start / re-spawn), rebuild the Confirmed-owned-
// target projection from the server (#145). The projection lives in memory and is
// lost on re-spawn; without this the index would stay EMPTY and passive/on-demand
// capture would be inert until the next pair. It fails closed (no credential or an
// unavailable read leaves the index empty).
void syncOwnedTargets();

// On worker (re)spawn the in-memory metric registry is empty and any per-boot
// gauge (e.g. queue_depth) has been lost. Re-derive queue depth from the
// AUTHORITATIVE durable queue — never an accumulated counter — and flush any
// telemetry batches that were persisted before the previous teardown. Fail-open:
// export never blocks capture.
void bootTelemetry();

async function bootTelemetry(): Promise<void> {
  await emitQueueDepth();
  await pumpTelemetry();
}

chrome.runtime.onInstalled.addListener(() => {
  chrome.alarms.create(FLUSH_ALARM, { periodInMinutes: 1 });
  // A HINT only (docs/09 closing note) — the actual bound is the server's
  // per-cycle allocation (EXT-012), never the alarm period itself.
  chrome.alarms.create(SCHEDULE_ALARM, { periodInMinutes: 15 });
});
// A browser restart re-spawns the worker with an empty in-memory projection —
// re-sync so capture is not silently inert after startup.
chrome.runtime.onStartup?.addListener(() => {
  void syncOwnedTargets();
});
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === FLUSH_ALARM) void flush().then(() => pumpTelemetry());
  if (a.name === SCHEDULE_ALARM) {
    // Periodically re-sync owned targets to pick up account/identity changes
    // (a newly Confirmed variant, or a de-confirmed one that must drop out).
    void syncOwnedTargets();
    void runScheduleCycleIfEnabled();
  }
});

chrome.runtime.onMessage.addListener((msg: ExtMessage, sender, sendResponse) => {
  void handle(msg, sender).then(sendResponse);
  return true; // async response
});

async function handle(msg: ExtMessage, sender: chrome.runtime.MessageSender): Promise<ExtResponse> {
  switch (msg.kind) {
    case "injectNavShim":
      return handleInjectNavShim(sender);
    case "capture":
      return handleCapture(msg.product, "passive");
    case "onDemandCapture":
      return handleOnDemandCapture(msg.product);
    case "addToWatchlist":
      return handleAddToWatchlist(msg.product);
    case "setScheduleEnabled":
      await store.set(KEY_SCHEDULE_ENABLED, msg.enabled);
      return { ok: true, state: await popupState() };
    case "getOverlayView":
      return handleGetOverlayView(msg.product);
    case "pair":
      return handlePair(msg.code);
    case "setEnabled":
      return handleSetEnabled(msg.enabled);
    case "revoke":
      return handleRevoke();
    case "getState":
      return { ok: true, state: await popupState() };
    case "retryDeadLetter":
      return handleRetryDeadLetter(msg.dedupKey);
    case "discardDeadLetter":
      return handleDiscardDeadLetter(msg.dedupKey);
  }
}

// credentialFingerprint is a stable, NON-LOGGED identity of the credential that
// launched a sync. It is built from the credential-scoped IDENTITY fields
// (credentialId + marketplaceAccountId) — never the raw capture secret — and is
// compared only in memory (identity quarantine: a sync result is applied only if
// the stored credential STILL matches the identity that started the request). It
// is never emitted to logs or metrics.
function credentialFingerprint(cred: PairingCredential): string {
  return `${cred.credentialId} ${cred.marketplaceAccountId}`;
}

// isCurrentSync re-reads the stored credential AFTER the network await and returns
// true only if `gen` is still the newest generation AND the stored credential's
// fingerprint still matches the snapshot taken before the request. The generation
// comparison runs AFTER the async read so a revoke/pair that landed during the
// read is observed. Cancellation (AbortController) races completion, so this
// generation+identity check is kept regardless of any abort.
async function isCurrentSync(gen: number, fp: string | null): Promise<boolean> {
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  const currentFp = cred ? credentialFingerprint(cred) : null;
  return gen === syncGeneration && currentFp === fp;
}

// syncOwnedTargets refreshes the local Confirmed-owned-target projection from the
// SERVER-AUTHORITATIVE credential-scoped read (#145, EXT-004). The marketplace
// account is derived server-side from the stored capture credential — never
// chosen here. It binds every result to BOTH a monotonic generation and the
// credential identity that initiated it (issue #253), then applies it only if
// that generation is still current AND the stored credential still matches —
// otherwise the completion is STALE and ignored (never applied over a newer
// projection, never used to clear a valid one). It FAILS CLOSED at every step:
//   - no stored credential ⇒ clear the index (nothing is owned until paired);
//   - a null result (401 revoked/expired, 5xx, network) ⇒ clear the index —
//     never keep a stale/guessed set, so capture stays disabled rather than
//     resurrecting a de-confirmed mapping;
//   - a real result ⇒ ATOMICALLY replace the whole index (the server is the sole
//     authority; the extension never merges partial owned sets), stamped with the
//     owning credential-account identity + generation.
async function syncOwnedTargets(): Promise<void> {
  const gen = ++syncGeneration;
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);

  if (!cred) {
    // Fail closed: nothing is owned until paired. Clear only if this sync is still
    // current — a concurrent pair may have raced a credential in during the read.
    if (await isCurrentSync(gen, null)) {
      ownedTargets.replaceAll([], { generation: gen, marketplaceAccountId: null });
    }
    return;
  }

  const fp = credentialFingerprint(cred);
  const result = await gateway.fetchOwnedTargets(cred.credential);

  // Stale-completion guard: apply the result (success OR failure) ONLY if this
  // generation is still the newest AND the stored credential still matches the
  // identity that launched the request. Otherwise the credential was revoked or
  // replaced while the request was in flight — ignore it and record `stale` so
  // the drop is observable, WITHOUT logging any credential secret.
  if (!(await isCurrentSync(gen, fp))) {
    incr("owned_targets_sync", { outcome: "stale" });
    return;
  }

  const owner = { generation: gen, marketplaceAccountId: cred.marketplaceAccountId };
  if (result === null) {
    // Fail closed: clear rather than retain stale/guessed ownership.
    ownedTargets.replaceAll([], owner);
    incr("owned_targets_sync", { outcome: "unavailable" });
    return;
  }
  ownedTargets.replaceAll(result, owner);
  gauge("owned_targets_count", result.length);
  incr("owned_targets_sync", { outcome: "ok" });
}

// Diagnostic-only, capability-gated page-context injection (docs/09). Gated on
// capability === "ready" — Unknown (never paired) / disabled / revoked NEVER
// triggers MAIN-world code injection into the page (Unknown never enables
// dependent logic, PRD §4.6). Targets ONLY the sender's OWN tab — never an
// enumerated/other tab (the extension holds no "tabs" permission and never
// asks for one). A missing tab id (e.g. a non-tab sender) is a no-op, never a
// thrown error across the message seam.
async function handleInjectNavShim(sender: chrome.runtime.MessageSender): Promise<ExtResponse> {
  const capability = await getCapability();
  if (capability !== "ready") return { ok: true };
  const tabId = sender.tab?.id;
  if (tabId === undefined) return { ok: true };
  try {
    await chrome.scripting.executeScript({
      target: { tabId },
      world: "MAIN",
      files: ["nav-shim.js"],
    });
  } catch (e) {
    log("warn", "nav_shim_inject_failed", { error: e instanceof Error ? e.message : "unknown" });
  }
  return { ok: true };
}

async function handleCapture(
  product: ParsedProduct,
  subRoute: "passive" | "on_demand" | "watchlist",
): Promise<ExtResponse> {
  const capability = await getCapability();
  const decision = prepareCapture(
    product,
    ownedTargets,
    capability,
    new Date().toISOString(),
    subRoute,
  );
  if (decision.action === "skip") {
    log("info", "capture_skipped", { reason: decision.reason });
    return { ok: true, state: await popupState() };
  }
  const r = await queue.enqueue(decision.capture);
  if (r.shed) incr("queue_backpressure");
  await emitQueueDepth();
  await flush();
  return { ok: true, state: await popupState() };
}

// EXT-003: on-demand refresh for the product the user is CURRENTLY viewing.
// It reuses the exact same gate + queue + immediate-flush path as passive
// capture — the only difference is the sub-route attribution (OBS-005) — so it
// inherits the ≤10s bound: enqueue and flush happen synchronously in this
// same message handler, never waiting for the 1-minute alarm hint.
// DEFERRED (named, not silently dropped): a REAL network-inclusive ≤10s
// timing proof against the live gateway belongs in S32's
// `task test:integration` (compose-based). service-worker.test.ts carries a
// bounded LOCAL proxy (stubbed network) proving this code path itself adds no
// artificial delay.
async function handleOnDemandCapture(product: ParsedProduct): Promise<ExtResponse> {
  const startedAt = Date.now();
  const result = await handleCapture(product, "on_demand");
  incr("on_demand_latency_ms", {}, Date.now() - startedAt);
  return result;
}

// EXT-007: add a Confirmed owned target to the priority watchlist. Gated on
// capability === "ready" FIRST (EXT-009 kill switch: a disabled/revoked/never-
// paired extension must never reach the server, even for a Confirmed-owned
// product) — then resolved through the SAME Confirmed-owned-target gate as
// capture (EXT-004: a NeedsReview/unmapped product NEVER reaches the
// watchlist). The server enforces the cap and audits the change (this handler
// NEVER self-certifies success — see watchlist.ts for the current fail-closed
// seam pending S37).
async function handleAddToWatchlist(product: ParsedProduct): Promise<ExtResponse> {
  const capability = await getCapability();
  if (capability !== "ready") return { ok: true, watchlist: { ok: false, reason: "denied" } };
  const target = ownedTargets.resolve(product);
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  if (!cred || !target) return { ok: true, watchlist: { ok: false, reason: "denied" } };
  const outcome = await watchlistGateway.addToWatchlist({
    credential: cred.credential,
    marketplaceAccountId: cred.marketplaceAccountId,
    variantId: target.variantId,
  });
  incr("watchlist_add", { outcome: outcome.ok ? "accepted" : outcome.reason });
  return { ok: true, watchlist: outcome };
}

// EXT-005: overlay data for the product being viewed. Gated on capability ===
// "ready" FIRST (EXT-009 kill switch — a disabled/revoked/never-paired
// extension never reaches the server), then resolved through the SAME
// Confirmed-owned-target gate (EXT-004). Rendered, never recomputed. See
// overlay-read.ts for the current fail-closed seam (captureAuth is
// capture-only; a genuine, named contract-scope gap).
async function handleGetOverlayView(product: ParsedProduct): Promise<ExtResponse> {
  const capability = await getCapability();
  if (capability !== "ready") return { ok: true, overlay: { kind: "unavailable" } };
  const target = ownedTargets.resolve(product);
  if (!target) return { ok: true, overlay: { kind: "unavailable" } };
  const result = await overlayReadGateway.fetchOverlayData(target.targetId);
  if (!result.ok) return { ok: true, overlay: { kind: "unavailable" } };
  // EXT-008: thread the tenant-authorized relevant-event id from the read seam
  // (null when the server reports none) so the overlay can offer an Event deep
  // link ONLY when a real gateway id exists — never a guessed/DK-native id.
  const view = deriveOverlayView(
    result.target,
    result.offers,
    Date.now(),
    result.relevantEventId ?? null,
  );

  // EXT-006: price history — gap-preserving, from the SAME fail-closed-seam
  // discipline as overlay-read.ts. `history` is null (never fabricated) when
  // the read seam isn't available yet.
  const historyResult = await historyReadGateway.fetchHistory(target.targetId);
  const history = historyResult.ok
    ? buildHistorySeries(historyResult.observations, historyResult.gapThresholdSeconds)
    : null;

  return { ok: true, overlay: { kind: "ready", view, history } };
}

// EXT-012: opt-in bounded scheduled refresh. `chrome.alarms` is only a
// scheduling HINT — every cycle asks the server for an allocation and NEVER
// exceeds it; a circuit-stop (or the fail-closed default allocation gateway,
// see schedule.ts) yields ZERO requests for the cycle.
async function runScheduleCycleIfEnabled(): Promise<void> {
  const enabled = (await store.get<boolean>(KEY_SCHEDULE_ENABLED)) ?? false;
  if (!enabled) return;
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  const capability = await getCapability();
  if (!cred || capability !== "ready") return; // fail closed — same posture as flush()
  await runScheduledCycle(cred.marketplaceAccountId, pendingAllocationGateway, async (target) => {
    // NEVER attaches a DK session credential/cookie to a scheduled request
    // (unlike the content script's deliberate own-session read) — fetched
    // fresh, unauthenticated, from the service worker via a REAL allocation
    // target once the allocation gateway is wired past its fail-closed stub.
    void target;
  });
}

async function handlePair(code: string): Promise<ExtResponse> {
  try {
    const cred: PairingCredential = await gateway.claimPairing(code);
    // Persist ONLY the allow-listed capture-credential fields (EXT-001).
    await store.set(KEY_CREDENTIAL, sanitizeCredential(cred));
    // Credential replacement (issue #253): invalidate every in-flight sync
    // BEFORE the new one runs, so a delayed prior-credential response can never
    // overwrite (or clear) the projection this pairing is about to install. The
    // fingerprint check already rejects a foreign-credential result; this bump
    // additionally rejects a same-account replacement whose generation is stale.
    syncGeneration++;
    await setCapability("ready");
    // Immediately populate the Confirmed-owned-target projection from the server
    // so passive/on-demand capture is live right after pairing — never inert
    // until the next alarm (#145). fail-closed: pairing reports `ready`, but the
    // EXT-004 gate resolves a target only from THIS credential's installed
    // projection; a stale prior sync can never make it resolve otherwise.
    await syncOwnedTargets();
    log("info", "paired");
    return { ok: true, state: await popupState() };
  } catch (e) {
    log("error", "pair_failed", { error: e instanceof Error ? e.message : "unknown" });
    return { ok: false, error: "pair_failed" };
  }
}

async function handleSetEnabled(enabled: boolean): Promise<ExtResponse> {
  const cap = await getCapability();
  // Only toggle between ready/disabled when a credential exists; never promote
  // out of unknown/revoked via the toggle (Unknown never enables).
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  if (!cred) return { ok: true, state: await popupState() };
  if (enabled && (cap === "disabled" || cap === "ready")) await setCapability("ready");
  if (!enabled) await setCapability("disabled");
  return { ok: true, state: await popupState() };
}

async function handleRevoke(): Promise<ExtResponse> {
  // Invalidate every outstanding sync FIRST (issue #253): bump the generation and
  // clear the index synchronously, BEFORE the async storage writes below, so an
  // in-flight request that completes during those awaits can neither repopulate
  // the cleared index (its generation is now stale) nor be treated as current.
  syncGeneration++;
  // A revoked credential must not leave a stale Confirmed-owned-target index
  // behind: capability alone already fail-closes handleAddToWatchlist/
  // handleGetOverlayView, but clearing the index too means there is no
  // window where a re-pair (before syncOwnedTargets re-runs) could ever
  // resolve a target from PRE-revocation state.
  ownedTargets.replaceAll([], { generation: syncGeneration, marketplaceAccountId: null });
  await store.remove(KEY_CREDENTIAL);
  await setCapability("revoked");
  log("info", "credential_cleared");
  return { ok: true, state: await popupState() };
}

// Operator recovery for exhausted deliveries (issue #150 / EXT-009). Retry
// returns a dead-lettered item to the pending queue and re-attempts delivery
// (flush() self-gates on capability, so a disabled/revoked extension still
// re-queues but never reaches the server); discard removes it intentionally.
// Both emit a distinct metric so the recovery action is observable, never a
// silent mutation.
async function handleRetryDeadLetter(dedupKey: string): Promise<ExtResponse> {
  const { moved, shed } = await queue.retryDeadLetter(dedupKey);
  incr("dead_letter_retry", { outcome: moved ? "moved" : "not_found" });
  // A retry at the queue cap shifts out the oldest live capture — signal it as
  // backpressure exactly like the enqueue path, so telemetry can tell a clean
  // recovery from one that shed a pending capture (issue #150, BLOCKER 2).
  if (shed) incr("queue_backpressure");
  if (moved) {
    await emitQueueDepth();
    await flush();
  }
  return { ok: true, state: await popupState() };
}

async function handleDiscardDeadLetter(dedupKey: string): Promise<ExtResponse> {
  const removed = await queue.discardDeadLetter(dedupKey);
  incr("dead_letter_discard", { outcome: removed ? "removed" : "not_found" });
  return { ok: true, state: await popupState() };
}

async function flush(): Promise<void> {
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  const capability = await getCapability();
  if (!cred || capability !== "ready") return; // fail closed
  const res = await queue.flush((capture) => gateway.uploadCapture(cred.credential, capture));
  if (res.accepted > 0) {
    await store.set(KEY_LAST_UPLOAD, new Date().toISOString());
    incr("upload_accepted", {}, res.accepted);
  }
  // Permanent 4xx drops and exhausted dead-letters are DISTINCT outcomes — each
  // gets its own metric so the popup + telemetry can tell an intentional drop
  // from a recoverable, preserved failure (issue #150).
  if (res.dropped > 0) incr("upload_failed", {}, res.dropped);
  if (res.deadLettered > 0) incr("upload_dead_letter", {}, res.deadLettered);
  if (res.revoked) {
    // Credential killed server-side: fail closed and surface a disabled state.
    await setCapability("revoked");
    incr("capability_transition", { to: "revoked" });
    log("warn", "upload_revoked");
  }
  await emitQueueDepth();
}

// emitQueueDepth reports the REAL pending-item count (docs/14: queue depth is a
// tracked metric) — never a placeholder. Read fresh from storage so it reflects
// enqueue/shed/flush that just happened.
async function emitQueueDepth(): Promise<void> {
  const depth = await queue.count();
  gauge("queue_depth", depth);
}

// pumpTelemetry persists the CURRENT in-memory metric registry into the durable
// outbox and attempts an export (issue #162). It is fail-open by construction:
// snapshot writes a bounded, allow-listed batch to chrome.storage (surviving the
// next worker teardown), and export drains only server-accepted batches through
// the injectable transport — a failing/absent transport leaves batches durably
// pending within the cap. Callers invoke it fire-and-forget so a capture path is
// NEVER blocked or degraded by telemetry.
async function pumpTelemetry(): Promise<void> {
  try {
    await telemetry.snapshot(snapshotMetrics(), new Date().toISOString());
    await telemetry.export(telemetryTransport);
  } catch (e) {
    // Telemetry is advisory (lowest load-shedding priority) — never surface a
    // failure onto the capture path.
    log("warn", "telemetry_pump_failed", { error: e instanceof Error ? e.message : "unknown" });
  }
}

async function getCapability(): Promise<Capability> {
  return (await store.get<Capability>(KEY_CAPABILITY)) ?? "unknown";
}
async function setCapability(c: Capability): Promise<void> {
  await store.set(KEY_CAPABILITY, c);
}

async function popupState(): Promise<PopupState> {
  const capability = await getCapability();
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  return {
    capability,
    marketplaceAccountId: cred?.marketplaceAccountId ?? null,
    lastUploadAt: (await store.get<string>(KEY_LAST_UPLOAD)) ?? null,
    queuedCount: await queue.count(),
    degradation: degradationReason(capability),
    scheduleEnabled: (await store.get<boolean>(KEY_SCHEDULE_ENABLED)) ?? false,
    deadLetter: await queue.deadLetterSummaries(),
  };
}
