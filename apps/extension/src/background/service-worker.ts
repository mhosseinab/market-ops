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
import {
  nextRevocationAttemptAt,
  revocationAttemptsExhausted,
  revocationPendingAgeExceeded,
  revocationRetryDue,
} from "../lib/revocation-backoff";
import { pendingAllocationGateway, runScheduledCycle } from "../lib/schedule";
import { initDevErrorReporting } from "../lib/spotlight";
import {
  chromeLocalStore,
  KEY_CAPABILITY,
  KEY_CREDENTIAL,
  KEY_LAST_UPLOAD,
  KEY_REVOCATION_PENDING,
  KEY_REVOCATION_UNCONFIRMED,
  type PendingRevocation,
  type PopupState,
  sanitizeCredential,
  type UnconfirmedRevocation,
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

// In-memory fail-closed gate for a user Revoke whose DURABLE writes failed
// (issue #149, F1). chrome.storage.local.set can reject (QUOTA_BYTES);
// handlePair was already guarded for exactly that, while the user-initiated kill
// switch was not — so a storage rejection left capture ENABLED, emitted no
// telemetry, and let the next scheduled refresh keep using the credential the
// user had just asked to revoke. Storage is precisely what is broken on that
// path, so the gate cannot itself be durable: it is a memory-only, one-way
// switch that can only make the capability MORE restrictive, and it is cleared
// only by a successful re-pair. It fails closed across a worker restart too — a
// respawn finds no credential change and simply reports whatever the (unwritten)
// durable state says, which is never MORE permissive than before the revoke.
let localCaptureLock = false;

// The BOUNDED evidence token recorded when a pending revoke is quarantined by
// its AGE bound rather than by an attempt's result: no attempt ever produced an
// answer, so no per-attempt evidence exists to carry over. It sits alongside the
// gateway's RevocationEvidence tokens in the quarantine record's `evidence`
// field, and like them it is locale-neutral and never interpolated.
const AGED_OUT_EVIDENCE = "unconfirmed_aged_out";

void initDevErrorReporting("service-worker");

// On service-worker start (cold start / re-spawn), rebuild the Confirmed-owned-
// target projection from the server (#145). The projection lives in memory and is
// lost on re-spawn; without this the index would stay EMPTY and passive/on-demand
// capture would be inert until the next pair. It fails closed (no credential or an
// unavailable read leaves the index empty).
void syncOwnedTargets();

// On service-worker start, resume any revocation the server has NOT yet
// confirmed (issue #149). A pending revoke is durable state, not an in-memory
// intention: an MV3 teardown between "user pressed Revoke" and "the authority
// invalidated the credential" must never silently drop the kill switch.
void retryPendingRevocationSafely();

// On service-worker start, resume any revocation that has been QUARANTINED as
// "could not confirm" (issue #149, fix 3). The quarantine is a durable state,
// not a graveyard: the revoke keeps being retried with the quarantined
// credential under the same bounded backoff, so a gateway that comes back (or
// finishes its rollout) still gets to confirm the kill switch.
void retryQuarantinedRevocationSafely();

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
  // A browser restart must also resume an unconfirmed revocation (#149).
  void retryPendingRevocationSafely();
  void retryQuarantinedRevocationSafely();
});
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === FLUSH_ALARM) {
    // Two INDEPENDENT chains (issue #149, G3). Chaining the durable upload-queue
    // flush downstream of the revocation retry meant a storage rejection inside
    // the retry (chrome.storage.local.set can reject, e.g. QUOTA_BYTES) escaped
    // the void-ed promise and skipped flush() + pumpTelemetry() for that tick —
    // and because such a write fails deterministically, the queue stopped
    // draining indefinitely with no observable signal. It also inverted
    // CLAUDE.md's load-shedding priority by letting the revocation path starve
    // the upload path.
    void retryPendingRevocationSafely();
    // A THIRD independent chain, for the same reason: a quarantined revoke must
    // keep being pursued, and its storage writes must never starve the others.
    void retryQuarantinedRevocationSafely();
    void flush().then(pumpTelemetry);
  }
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
//   - capability is not READY (disabled, revoked, or an UNCONFIRMED revocation
//     still pending) ⇒ clear the index and return WITHOUT touching the gateway;
//   - a null result (401 revoked/expired, 5xx, network) ⇒ clear the index —
//     never keep a stale/guessed set, so capture stays disabled rather than
//     resurrecting a de-confirmed mapping;
//   - a real result ⇒ ATOMICALLY replace the whole index (the server is the sole
//     authority; the extension never merges partial owned sets), stamped with the
//     owning credential-account identity + generation.
//
// The capability gate is load-bearing, not defensive tidiness (issue #149): the
// credential is deliberately RETAINED while a revoke is unconfirmed, so gating
// on credential presence alone kept issuing owned-target reads with the exact
// credential the user had just asked to revoke — for up to its 30-day TTL — and
// repopulated the index handleRevoke had cleared. Capture must consume nothing
// from a credential that is on its way out.
async function syncOwnedTargets(): Promise<void> {
  const gen = ++syncGeneration;
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  const capability = await getCapability();

  if (!cred || capability !== "ready") {
    // Fail closed: nothing is owned until paired AND ready. Clear only if this
    // sync is still current — a concurrent pair may have raced a credential in
    // during the read.
    const currentFp = cred ? credentialFingerprint(cred) : null;
    if (await isCurrentSync(gen, currentFp)) {
      ownedTargets.replaceAll([], { generation: gen, marketplaceAccountId: null });
    }
    if (cred) incr("owned_targets_sync", { outcome: "not_ready" });
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
  // #149: never abandon a credential whose revocation the server has not
  // confirmed — re-pairing would overwrite the ONLY material a retry can use,
  // leaving a live credential in the wild. Try once more to settle it; if it is
  // still pending, refuse (fail closed, visibly) rather than pair over it.
  // `force` bypasses the retry backoff: this is a USER action, not a timer tick,
  // so it is neither lockstep fleet load nor something to make the user wait out.
  // Guarded (G3): a storage failure here must not reject the message handler and
  // leave the popup without a response. It fails CLOSED — the marker survives, so
  // the refusal below still blocks pairing over an unsettled revocation.
  await retryPendingRevocationSafely({ force: true });
  if (await store.get<PendingRevocation>(KEY_REVOCATION_PENDING)) {
    log("warn", "pair_blocked_revocation_pending");
    return { ok: false, error: "revocation_pending" };
  }
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
    // A successful re-pair is the ONLY thing that clears the F1 in-memory gate:
    // the durable state has just been written successfully, so storage works and
    // this credential is genuinely live.
    localCaptureLock = false;
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
  // #149: an unconfirmed revocation is never toggled away. Capture stays off
  // until the authority confirms (or the credential expires) — the toggle can
  // neither re-enable capture nor mask the pending state as a plain disable.
  if (cap === "revocation_pending") return { ok: true, state: await popupState() };
  // Only toggle between ready/disabled when a credential exists; never promote
  // out of unknown/revoked via the toggle (Unknown never enables).
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  if (!cred) return { ok: true, state: await popupState() };
  if (enabled && (cap === "disabled" || cap === "ready")) {
    await setCapability("ready");
    // syncOwnedTargets now fails closed on a non-ready capability (#149), so a
    // worker that started while capture was disabled holds an EMPTY projection.
    // Re-sync on the way back to ready rather than leaving capture inert until
    // the next 15-minute alarm.
    await syncOwnedTargets();
  }
  if (!enabled) await setCapability("disabled");
  return { ok: true, state: await popupState() };
}

// EXT-009 kill switch (issue #149). Local deletion is CLEANUP, not revocation:
// the credential must be invalidated at the authority that verifies it, or a
// copied credential keeps uploading after the user believes access was revoked.
//
// Order matters and is fail-closed at every step:
//   1. capture is disabled IMMEDIATELY (index cleared + capability off) — before
//      any network call, and regardless of how that call goes;
//   2. the revocation intent is persisted DURABLY as the FIRST write — before
//      the capability write and before the request — so an MV3 teardown at any
//      point after the user pressed Revoke cannot lose it. The marker is also
//      AUTHORITATIVE over the stored capability (see getCapability), so the
//      mirror interleaving (marker written, capability write lost) still keeps
//      capture off rather than leaving a window where it is nominally ready;
//   3. the credential material is discarded ONLY once the server confirms (or
//      the credential's authoritative expiry is reached) — a failed revoke keeps
//      it, because it is the only thing a retry can be made with;
//   4. `revocation_pending` is reported as VISIBLY DISTINCT from `revoked` — the
//      popup never claims a kill switch that has not actually killed anything.
//
// Every durable write below is GUARDED (issue #149, F1). chrome.storage.local
// .set can reject (QUOTA_BYTES); handlePair was already guarded for exactly
// that, while the user-initiated kill switch — the one path that must never fail
// open — was left bare. An unguarded rejection escaped `handle().then(
// sendResponse)`, so the popup received NO response, the capability stayed
// `ready`, nothing was counted, and the next scheduled refresh re-synced owned
// targets with the credential being revoked.
async function handleRevoke(): Promise<ExtResponse> {
  try {
    return await revokeLocked();
  } catch (e) {
    // The durable record of the revoke could not be written. Fail CLOSED in
    // memory — storage is exactly what is broken, so an in-memory gate is the
    // only honest option — respond to the popup regardless, and COUNT it: a
    // fallback engaging with nothing emitted is always a bug (CLAUDE.md).
    localCaptureLock = true;
    incr("credential_revocation", { outcome: "local_storage_error" });
    incr("capability_transition", { to: "revocation_unconfirmed" });
    log("error", "credential_revocation_storage_error", {
      error: e instanceof Error ? e.message : "unknown",
    });
    return { ok: true, state: await safePopupState() };
  }
}

async function revokeLocked(): Promise<ExtResponse> {
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

  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  if (!cred) {
    // No local credential material: there is nothing to present to the server
    // and nothing to retry with. Repeating a completed revoke lands here — it is
    // idempotent, ending in the same visible revoked state.
    //
    // This branch reaches a terminal `revoked` with NO server confirmation, so
    // it must be observable and distinguishable (issue #149, G5). It previously
    // emitted no counter at all, while the timer-driven sibling emitted
    // `orphaned` for the identical situation — telemetry could not tell a
    // user-initiated orphan resolution from a genuine confirmed revocation.
    // The two cases get DISTINCT outcomes:
    //   orphaned        — a durable marker was live and is being discarded here,
    //                     so a revoke the user really asked for is ending
    //                     without the authority ever confirming it;
    //   already_cleared — no marker, no credential: a repeat of a revoke that
    //                     already completed. Idempotent, not a lost revocation.
    const hadMarker = Boolean(await store.get<PendingRevocation>(KEY_REVOCATION_PENDING));
    await store.remove(KEY_REVOCATION_PENDING);
    await setCapability("revoked");
    incr("capability_transition", { to: "revoked" });
    if (hadMarker) {
      incr("credential_revocation", { outcome: "orphaned" });
      log("warn", "credential_revocation_orphaned", { hadCredential: false });
    } else {
      incr("credential_revocation", { outcome: "already_cleared" });
      log("info", "credential_cleared", { outcome: "already_cleared" });
    }
    return { ok: true, state: await popupState() };
  }

  // Capture is off from here on, whatever the server says. The DURABLE marker
  // is written FIRST: a teardown after it but before the capability write still
  // leaves an unambiguous, retryable record of the user's intent (and
  // getCapability treats that marker as authoritative, so capture is off
  // either way). The reverse order left a window in which the popup claimed
  // "awaiting confirmation" with nothing durable to retry from.
  const pending: PendingRevocation = {
    requestedAt: new Date().toISOString(),
    credentialId: cred.credentialId,
    marketplaceAccountId: cred.marketplaceAccountId,
    credentialExpiresAt: cred.expiresAt,
    attempts: 0,
    serverContacted: false,
  };
  await store.set(KEY_REVOCATION_PENDING, pending);
  await setCapability("revocation_pending");
  await settlePendingRevocation(pending, cred);
  return { ok: true, state: await popupState() };
}

// settlePendingRevocation attempts ONE server revocation for a durably recorded
// pending revoke and resolves it only on an authoritative answer.
//
// The ONLY route to a terminal `revoked` here is `result.outcome === "confirmed"`
// (a 2xx, or a 401 meaning the credential no longer authenticates anything).
// The DEVICE clock cannot produce one (issue #149, G1): the expiry shortcut used
// to run BEFORE the request, so a forward-skewed clock discarded the credential,
// cleared the marker and reported the kill switch complete — while skipping an
// authoritative answer that was available right then. Requiring a prior gateway
// contact did not fix that: ANY real HTTP response (including a 503 during a
// deploy) sets `serverContacted`, and reaching the gateway is no evidence the
// clock is right.
async function settlePendingRevocation(
  pending: PendingRevocation,
  cred: PairingCredential,
  opts: { force?: boolean } = {},
): Promise<void> {
  // Count the attempt BEFORE the request so a teardown mid-flight still records
  // that one was made (the marker is the durable record, not the response), and
  // schedule the next retry from that count so the backoff is durable too.
  const attempts = pending.attempts + 1;
  const attempted: PendingRevocation = {
    ...pending,
    attempts,
    nextAttemptAt: nextRevocationAttemptAt(attempts, pending.credentialId, Date.now()),
  };
  await store.set(KEY_REVOCATION_PENDING, attempted);

  const result = await gateway.revokeCredential(cred.credential);
  if (result.reachedServer && !attempted.serverContacted) {
    attempted.serverContacted = true;
    await store.set(KEY_REVOCATION_PENDING, attempted);
  }
  if (result.outcome === "confirmed") {
    await finalizeRevocation();
    return;
  }

  // A FORCED (user-initiated) attempt that never reached the server does not
  // consume the AUTHORITATIVE attempt budget (issue #149, fix 3). That budget
  // exists to bound how long we keep asking an authority that keeps answering
  // non-authoritatively; a transport failure is not an answer. A reviewer probe
  // showed an OFFLINE user pressing Pair draining 47 of the 48 attempts this
  // way — deterministic, no clock skew — turning a user's retry into the thing
  // that ended their own revoke. The marker's own AGE bound (checked in
  // retryPendingRevocation) is what keeps this from being unbounded.
  const forcedWithoutContact = Boolean(opts.force) && !result.reachedServer;
  if (forcedWithoutContact) {
    await store.set(KEY_REVOCATION_PENDING, { ...attempted, attempts: pending.attempts });
    incr("credential_revocation", { outcome: "forced_no_contact" });
    log("warn", "credential_revocation_forced_no_contact", { evidence: result.evidence });
    return;
  }

  // No authoritative answer. If the credential ALSO looks expired by the device
  // clock, that changes nothing about the outcome — it is recorded only so the
  // refusal is observable. A reachable gateway answers 401 for a genuinely
  // expired credential, which is a real confirmation and was already handled
  // above; anything else means we simply do not know.
  const expiresAt = Date.parse(cred.expiresAt);
  if (Number.isFinite(expiresAt) && expiresAt <= Date.now()) {
    incr("credential_revocation", { outcome: "expiry_unverified" });
    log("warn", "credential_revocation_expiry_unverified", {
      attempts,
      reachedServer: result.reachedServer,
    });
  }

  // The marker's CLOCK-INDEPENDENT bound: the attempt count only advances when a
  // real request came back non-authoritative. When it is spent, stop holding the
  // PENDING marker and move the revoke into the "could not confirm" QUARANTINE —
  // never `revoked` (no authority confirmed anything) and never a silent discard
  // of the credential (the revoke keeps being retried there).
  // …and the marker's AGE bound, applied only AFTER the attempt so an available
  // authority is never skipped (G1). Whichever bound fires, the terminal is the
  // same honest quarantine.
  const aged = revocationPendingAgeExceeded(pending, Date.now());
  if (revocationAttemptsExhausted(attempts) || aged) {
    await quarantineUnconfirmedRevocation(
      { ...attempted, attempts },
      cred,
      result.evidence,
      aged ? "age_exceeded" : "attempts_exhausted",
    );
    return;
  }

  // No authoritative answer: keep the credential material (needed to retry),
  // keep capture disabled, and stay VISIBLY pending. Never a silent success.
  incr("credential_revocation", { outcome: "pending" });
  log("warn", "credential_revocation_pending", {
    attempts,
    reachedServer: result.reachedServer,
    evidence: result.evidence,
  });
}

// finalizeRevocation is the ONLY place a CONFIRMED revocation ends: the
// authority itself stated the credential is dead (2xx, or a 401 meaning it no
// longer authenticates anything). It is therefore the only transition entitled
// to report the kill switch complete.
//
// It mirrors handleRevoke's projection teardown (bump the generation, clear the
// Confirmed-owned-target index) rather than only removing the credential. A
// pending revoke retains its credential, so an index populated BEFORE the
// revocation could otherwise survive finalization and resolve a
// PRE-revocation target after a re-pair — potentially uploading account A's
// target on a request authenticated with account B's credential (identity
// quarantine, §4.6).
async function finalizeRevocation(): Promise<void> {
  await tearDownCredential();
  await setCapability("revoked");
  incr("credential_revocation", { outcome: "confirmed" });
  incr("capability_transition", { to: "revoked" });
  log("info", "credential_cleared", { outcome: "confirmed" });
}

// quarantineUnconfirmedRevocation is the explicit "COULD NOT CONFIRM" terminal
// (issue #149, fix 3) — the replacement for the previous
// `abandonUnconfirmedRevocation`, which DISCARDED the credential outright and
// reported `unknown`. That discard defeated EXT-009: the server row may well be
// live for its remaining TTL, and destroying the only material a retry could use
// guaranteed it would never be killed. §4.6 calls for quarantine over inference.
//
// So the revoke is quarantined, not abandoned:
//   - the credential material is RELOCATED into a durable, allow-list-audited
//     quarantine record (KEY_REVOCATION_UNCONFIRMED) that survives an MV3
//     restart, so the retry can continue for as long as the credential can
//     possibly be alive;
//   - the pending marker and KEY_CREDENTIAL are removed, which UNBLOCKS
//     re-pairing (handlePair refuses only on the pending marker) — a user is
//     never locked out by an authority that never answered;
//   - the owned-target projection is torn down with a generation bump, exactly
//     as tearDownCredential does (identity quarantine, §4.6);
//   - the capability becomes `revocation_unconfirmed`: fails closed, and is
//     VISIBLY distinct from `revoked` (a completed kill switch it did not
//     achieve) and from `revocation_pending`;
//   - it is AUDITED with its own metric outcome, its own capability transition,
//     and a structured log carrying the BOUNDED reason + evidence tokens. It is
//     never folded into `revoked` or into a generic failure.
//
// `reason` is a bounded, locale-neutral token: which of the two bounds fired.
async function quarantineUnconfirmedRevocation(
  pending: PendingRevocation,
  cred: PairingCredential,
  evidence: string,
  reason: "attempts_exhausted" | "age_exceeded",
): Promise<void> {
  const record: UnconfirmedRevocation = {
    credential: cred.credential,
    credentialId: cred.credentialId,
    marketplaceAccountId: cred.marketplaceAccountId,
    credentialExpiresAt: cred.expiresAt,
    requestedAt: pending.requestedAt,
    attempts: pending.attempts,
    evidence,
    nextAttemptAt: pending.nextAttemptAt,
  };
  await store.set(KEY_REVOCATION_UNCONFIRMED, record);
  syncGeneration++;
  ownedTargets.replaceAll([], { generation: syncGeneration, marketplaceAccountId: null });
  await store.remove(KEY_CREDENTIAL);
  await store.remove(KEY_REVOCATION_PENDING);
  await setCapability("revocation_unconfirmed");
  incr("credential_revocation", { outcome: "quarantined_unconfirmed" });
  incr("capability_transition", { to: "revocation_unconfirmed" });
  log("warn", "credential_revocation_quarantined", {
    attempts: pending.attempts,
    evidence,
    reason,
  });
}

// retryQuarantinedRevocationSafely is the ONLY way callers drive the quarantined
// retry. Like its pending-marker sibling it converts a storage rejection into a
// counted outcome and returns normally, so no caller's downstream work is
// skipped and no rejection escapes a void-ed promise.
async function retryQuarantinedRevocationSafely(): Promise<void> {
  try {
    await retryQuarantinedRevocation();
  } catch (e) {
    incr("credential_revocation", { outcome: "retry_error" });
    log("error", "credential_revocation_retry_error", {
      error: e instanceof Error ? e.message : "unknown",
    });
  }
}

// retryQuarantinedRevocation keeps pursuing a quarantined revoke on the flush
// alarm and across worker restarts. A no-op when nothing is quarantined.
async function retryQuarantinedRevocation(): Promise<void> {
  const q = await store.get<UnconfirmedRevocation>(KEY_REVOCATION_UNCONFIRMED);
  if (!q) return;

  // BOUNDED LIFETIME, non-silently. Past the credential's authoritative expiry
  // the credential cannot authenticate anything, so retrying is pointless and
  // retaining the material is a live secret kept for nothing. Discarding it is a
  // real state transition with its own metric + log — never a silent delete, and
  // never a terminal `revoked` (nothing was ever confirmed).
  const expiresAt = Date.parse(q.credentialExpiresAt);
  if (Number.isFinite(expiresAt) && expiresAt <= Date.now()) {
    await discardExpiredQuarantine(q);
    return;
  }

  if (!revocationRetryDue(q, Date.now())) {
    incr("credential_revocation", { outcome: "quarantine_deferred" });
    return;
  }

  // Count + schedule BEFORE the request, so a teardown mid-flight cannot reset
  // the backoff to one request per alarm tick.
  const attempts = q.attempts + 1;
  const attempted: UnconfirmedRevocation = {
    ...q,
    attempts,
    nextAttemptAt: nextRevocationAttemptAt(attempts, q.credentialId, Date.now()),
  };
  await store.set(KEY_REVOCATION_UNCONFIRMED, attempted);

  const result = await gateway.revokeCredential(q.credential);
  if (result.outcome === "confirmed") {
    await resolveQuarantine(result.evidence);
    return;
  }
  await store.set(KEY_REVOCATION_UNCONFIRMED, { ...attempted, evidence: result.evidence });
  incr("credential_revocation", { outcome: "quarantine_retry_pending" });
  log("warn", "credential_revocation_quarantine_pending", {
    attempts,
    evidence: result.evidence,
    reachedServer: result.reachedServer,
  });
}

// resolveQuarantine exits the quarantine HONESTLY: the authority finally
// confirmed, so the record is cleared and a DISTINCT outcome is emitted — a
// revocation confirmed late is not the same operational event as one confirmed
// on the spot, and telemetry must be able to tell them apart.
//
// The capability is touched ONLY if it is still `revocation_unconfirmed`. If the
// user has re-paired since (the quarantine deliberately does not block that),
// the capability belongs to a NEWER credential and resolving an OLD credential's
// revoke must never clobber it into `revoked`.
async function resolveQuarantine(evidence: string): Promise<void> {
  await store.remove(KEY_REVOCATION_UNCONFIRMED);
  incr("credential_revocation", { outcome: "confirmed_after_quarantine" });
  log("info", "credential_revocation_confirmed_after_quarantine", { evidence });
  if ((await rawCapability()) !== "revocation_unconfirmed") return;
  await setCapability("revoked");
  incr("capability_transition", { to: "revoked" });
}

// discardExpiredQuarantine ends a quarantined revoke at the credential's
// authoritative expiry. It lands on `unknown` ("not paired"), NEVER `revoked`:
// the credential aging out is not the authority confirming a revocation, and the
// popup must not claim a kill switch it never achieved.
async function discardExpiredQuarantine(q: UnconfirmedRevocation): Promise<void> {
  await store.remove(KEY_REVOCATION_UNCONFIRMED);
  incr("credential_revocation", { outcome: "quarantine_expired" });
  log("warn", "credential_revocation_quarantine_expired", {
    attempts: q.attempts,
    evidence: q.evidence,
  });
  if ((await rawCapability()) !== "revocation_unconfirmed") return;
  await setCapability("unknown");
  incr("capability_transition", { to: "unknown" });
}

// tearDownCredential is the ONE place capture-credential material is discarded.
// It bumps the sync generation and clears the Confirmed-owned-target projection
// FIRST, so no in-flight sync can repopulate an index that outlives the
// credential it belonged to (identity quarantine, §4.6).
async function tearDownCredential(): Promise<void> {
  syncGeneration++;
  ownedTargets.replaceAll([], { generation: syncGeneration, marketplaceAccountId: null });
  await store.remove(KEY_CREDENTIAL);
  await store.remove(KEY_REVOCATION_PENDING);
}

// retryPendingRevocation resumes an unconfirmed revocation across MV3 worker
// restarts and on the flush alarm. It is a no-op when nothing is pending.
//
// `force` bypasses the retry backoff for USER-initiated settles (re-pairing).
// Timer-driven callers never force: the alarm ticks once a minute in every
// installed extension at once, and an unbounded 1/min retry against a 30-day
// credential TTL is exactly the thundering herd CLAUDE.md's backpressure and
// rate-limiting rules forbid.
// retryPendingRevocationSafely is the ONLY way callers drive the retry (issue
// #149, G3). retryPendingRevocation writes to chrome.storage, and those writes
// CAN reject; an unhandled rejection would be a fallback engaging with nothing
// emitted, which CLAUDE.md classes as always a bug. It converts the failure into
// a counted, distinct outcome and returns normally, so no caller's downstream
// work is skipped. Failing here is safe: the durable marker is untouched, so the
// next due tick retries.
async function retryPendingRevocationSafely(opts: { force?: boolean } = {}): Promise<void> {
  try {
    await retryPendingRevocation(opts);
  } catch (e) {
    incr("credential_revocation", { outcome: "retry_error" });
    log("error", "credential_revocation_retry_error", {
      error: e instanceof Error ? e.message : "unknown",
    });
  }
}

async function retryPendingRevocation(opts: { force?: boolean } = {}): Promise<void> {
  let pending = await store.get<PendingRevocation>(KEY_REVOCATION_PENDING);
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);

  if (!pending) {
    // Reconciliation. A capability of `revocation_pending` with a stored
    // credential but NO durable marker is an inconsistent state: the user asked
    // to revoke, the popup says "awaiting confirmation", and yet nothing would
    // ever retry — the server credential would stay live until its own expiry.
    // Rebuild the marker from the credential rather than early-returning.
    if ((await rawCapability()) !== "revocation_pending") return;
    if (!cred) {
      // Neither a marker nor credential material survives, so nothing can ever
      // be retried and nothing can ever clear this state on its own — the popup
      // would report "awaiting confirmation" forever. Resolve fail-closed, with
      // the same DISTINCT outcome as the orphan branch below: this is a local
      // resolution, never a server confirmation.
      await demoteToRevoked();
      incr("credential_revocation", { outcome: "orphaned" });
      log("warn", "credential_revocation_orphaned", { hadCredential: false });
      return;
    }
    pending = {
      requestedAt: new Date().toISOString(),
      credentialId: cred.credentialId,
      marketplaceAccountId: cred.marketplaceAccountId,
      credentialExpiresAt: cred.expiresAt,
      attempts: 0,
      serverContacted: false,
    };
    await store.set(KEY_REVOCATION_PENDING, pending);
    incr("credential_revocation", { outcome: "marker_reconstructed" });
    log("warn", "credential_revocation_marker_reconstructed");
  }

  if (!cred || cred.credentialId !== pending.credentialId) {
    // The material this revoke needs is gone (or belongs to a different
    // credential), so no retry can ever succeed. Resolve to the fail-closed
    // revoked state rather than keep a marker that can never clear — but record
    // it as a DISTINCT outcome: this is the only path to `revoked` with no
    // server confirmation and no expiry check, and a fallback that engages
    // without an emitted event is always a bug (CLAUDE.md).
    await store.remove(KEY_REVOCATION_PENDING);
    await demoteToRevoked();
    incr("credential_revocation", { outcome: "orphaned" });
    log("warn", "credential_revocation_orphaned", { hadCredential: Boolean(cred) });
    return;
  }

  if (!opts.force && !revocationRetryDue(pending, Date.now())) {
    // Inside the backoff window. Before deferring, apply the marker's DURABLE
    // AGE BOUND (issue #149, fix 3). The attempt budget only advances when a
    // request was actually MADE, so a device with zero server contact could
    // otherwise hold this marker forever — and the pending marker BLOCKS
    // re-pairing. A user must never be locked out by an authority that never
    // answered.
    //
    // The check lives HERE, and after an attempt (see settlePendingRevocation),
    // never before one: a clock-driven bound must never SKIP an authority that
    // is sitting there ready to answer (G1). A clock-driven transition INTO the
    // quarantine is safe — it claims nothing about the server, retains the
    // credential and keeps retrying. What the device clock may never produce is
    // a terminal `revoked`; that invariant is untouched.
    if (revocationPendingAgeExceeded(pending, Date.now())) {
      await quarantineUnconfirmedRevocation(pending, cred, AGED_OUT_EVIDENCE, "age_exceeded");
      return;
    }
    // Deferred, not dropped. The marker (and its schedule) stay durable, so the
    // next due tick retries.
    incr("credential_revocation", { outcome: "deferred" });
    return;
  }
  await settlePendingRevocation(pending, cred, opts);
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

// rawCapability is the value AS STORED, with no reconciliation. Only the
// revocation state machine uses it — everything else must go through
// getCapability so it observes the pending-revocation override.
async function rawCapability(): Promise<Capability> {
  return (await store.get<Capability>(KEY_CAPABILITY)) ?? "unknown";
}

// getCapability is the single gate every capture/upload/UI path consults.
//
// A DURABLE pending-revocation marker OVERRIDES a stored `ready` (issue #149).
// The two writes of a revoke cannot be atomic in chrome.storage, so an MV3
// teardown can land between them; making the marker authoritative means capture
// is off from the instant the intent is durable, in either interleaving. It is
// deliberately a one-way override — it can only ever make the capability MORE
// restrictive, never promote out of unknown/disabled/revoked.
async function getCapability(): Promise<Capability> {
  const stored = await rawCapability();
  // The in-memory fail-closed gate for a Revoke whose durable writes failed
  // (issue #149, F1). Like the marker override it is ONE-WAY: it can only make
  // the capability more restrictive, never promote out of unknown/disabled/
  // revoked, and it reports the honest "could not confirm" state rather than a
  // `revocation_pending` it has nothing durable to retry from.
  if (localCaptureLock && (stored === "ready" || stored === "revocation_pending")) {
    return "revocation_unconfirmed";
  }
  if (stored !== "ready") return stored;
  const pending = await store.get<PendingRevocation>(KEY_REVOCATION_PENDING);
  return pending ? "revocation_pending" : stored;
}
async function setCapability(c: Capability): Promise<void> {
  await store.set(KEY_CAPABILITY, c);
}

// demoteToRevoked repairs the capability when a pending revocation is resolved
// LOCALLY (its credential material is gone, so no retry can ever succeed).
//
// It is a NEVER-PROMOTE check, not an equality check (issue #149, G4). Testing
// for `revocation_pending` alone missed the composed teardown the F6/F7 tests
// already establish: the durable marker survives while the capability write is
// lost, leaving the STORED capability at its pre-revoke `ready`. Removing the
// marker then dropped the only thing holding capture off, so right after a user
// revoke the popup rendered capture ON with no degradation note and nav-shim
// injection resumed on a just-revoked, unpaired extension.
//
// It can only ever make the capability MORE restrictive: `unknown`, `disabled`
// and `revoked` are left exactly as they are — the repair is not licence to
// rewrite an unrelated state.
async function demoteToRevoked(): Promise<void> {
  // Mirror the #253 owned-target teardown that handleRevoke and
  // tearDownCredential both perform (issue #149, F2). Without it an ORPHAN
  // resolution ended a revoke while the in-memory Confirmed-owned-target
  // projection SURVIVED, so a re-pair as another account could upload account
  // A's target on a request authenticated with account B's credential —
  // identity quarantine (§4.6). It runs UNCONDITIONALLY: clearing the index is
  // always the fail-closed direction, whatever the stored capability says.
  syncGeneration++;
  ownedTargets.replaceAll([], { generation: syncGeneration, marketplaceAccountId: null });
  const raw = await rawCapability();
  if (raw !== "ready" && raw !== "revocation_pending") return;
  await setCapability("revoked");
  incr("capability_transition", { to: "revoked" });
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
    // Issue #149, fix 3: an outstanding "could not confirm" revocation stays
    // VISIBLE even after a re-pair, when `capability` reads `ready` again and
    // there is no degradation to show. A credential that may still be live at
    // the authority must never become invisible (EXT-009).
    revocationUnconfirmed: Boolean(
      await store.get<UnconfirmedRevocation>(KEY_REVOCATION_UNCONFIRMED),
    ),
  };
}

// safePopupState is used ONLY on the guarded Revoke failure path (issue #149,
// F1): storage is broken there, so even reading it back may fail. It degrades to
// the honest, fail-closed "could not confirm" snapshot rather than letting the
// message handler reject and leave the popup with no response at all.
async function safePopupState(): Promise<PopupState> {
  try {
    return await popupState();
  } catch {
    return {
      capability: "revocation_unconfirmed",
      marketplaceAccountId: null,
      lastUploadAt: null,
      queuedCount: 0,
      degradation: degradationReason("revocation_unconfirmed"),
      scheduleEnabled: false,
      deadLetter: [],
      revocationUnconfirmed: true,
    };
  }
}
