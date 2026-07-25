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
  type PendingRevocation,
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

// On service-worker start, resume any revocation the server has NOT yet
// confirmed (issue #149). A pending revoke is durable state, not an in-memory
// intention: an MV3 teardown between "user pressed Revoke" and "the authority
// invalidated the credential" must never silently drop the kill switch.
void retryPendingRevocationSafely();

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

  // The marker's HARD lifetime bound, and it is CLOCK-INDEPENDENT: the attempt
  // count only advances when a real request came back non-authoritative. When it
  // is spent, stop retrying and terminate into `unknown` — NEVER `revoked`,
  // because no authority ever confirmed anything (see revocation-backoff.ts).
  if (revocationAttemptsExhausted(attempts)) {
    await abandonUnconfirmedRevocation(attempts);
    return;
  }

  // No authoritative answer: keep the credential material (needed to retry),
  // keep capture disabled, and stay VISIBLY pending. Never a silent success.
  incr("credential_revocation", { outcome: "pending" });
  log("warn", "credential_revocation_pending", { attempts, reachedServer: result.reachedServer });
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

// abandonUnconfirmedRevocation ends a revoke the authority NEVER confirmed, once
// its clock-independent attempt budget is spent (issue #149, G1).
//
// It deliberately does NOT reach `revoked`: nothing confirmed the credential is
// dead, and the server row may well be live for its real remaining TTL, so
// claiming a completed kill switch would be exactly the #149 defect. It lands on
// `unknown` — "not paired" — which is honest and, like every non-ready state,
// fails closed (Unknown never enables, PRD §4.6). The honest meaning "we could
// not confirm this" has no glossary term, so the distinction is carried by the
// METRIC (`abandoned_unconfirmed`), never by invented user-facing copy.
//
// The credential material is discarded with the marker: no further retry will
// ever be made with it, so retaining it would only keep a live secret on the
// device for nothing.
async function abandonUnconfirmedRevocation(attempts: number): Promise<void> {
  await tearDownCredential();
  await setCapability("unknown");
  incr("credential_revocation", { outcome: "abandoned_unconfirmed" });
  incr("capability_transition", { to: "unknown" });
  log("warn", "credential_revocation_abandoned", { attempts });
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
    // Inside the backoff window — deferred, not dropped. The marker (and its
    // schedule) stay durable, so the next due tick retries.
    incr("credential_revocation", { outcome: "deferred" });
    return;
  }
  await settlePendingRevocation(pending, cred);
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
  };
}
