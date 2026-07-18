import { type Capability, degradationReason } from "../lib/capability";
import { GatewayClient } from "../lib/gateway";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import { gauge, incr, log } from "../lib/observability";
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
const gateway = new GatewayClient(GATEWAY_BASE_URL);
const watchlistGateway = createWatchlistGateway();
const overlayReadGateway = createOverlayReadGateway();
// Confirmed owned targets: server-authoritative, starts EMPTY (fail closed —
// EXT-004). Populated by the S31 authenticated targets sync.
const ownedTargets = new OwnedTargetIndex();

void initDevErrorReporting("service-worker");

chrome.runtime.onInstalled.addListener(() => {
  chrome.alarms.create(FLUSH_ALARM, { periodInMinutes: 1 });
  // A HINT only (docs/09 closing note) — the actual bound is the server's
  // per-cycle allocation (EXT-012), never the alarm period itself.
  chrome.alarms.create(SCHEDULE_ALARM, { periodInMinutes: 15 });
});
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === FLUSH_ALARM) void flush();
  if (a.name === SCHEDULE_ALARM) void runScheduleCycleIfEnabled();
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
    case "setOwnedTargets":
      ownedTargets.replaceAll(msg.targets);
      return { ok: true };
  }
}

// Diagnostic-only, capability-gated page-context injection (docs/09). Targets
// ONLY the sender's OWN tab — never an enumerated/other tab (the extension
// holds no "tabs" permission and never asks for one). A missing tab id (e.g. a
// non-tab sender) is a no-op, never a thrown error across the message seam.
async function handleInjectNavShim(sender: chrome.runtime.MessageSender): Promise<ExtResponse> {
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
async function handleOnDemandCapture(product: ParsedProduct): Promise<ExtResponse> {
  const startedAt = Date.now();
  const result = await handleCapture(product, "on_demand");
  incr("on_demand_latency_ms", {}, Date.now() - startedAt);
  return result;
}

// EXT-007: add a Confirmed owned target to the priority watchlist. Resolved
// through the SAME Confirmed-owned-target gate as capture — EXT-004: a
// NeedsReview/unmapped product NEVER reaches the watchlist. The server
// enforces the cap and audits the change (this handler NEVER self-certifies
// success — see watchlist.ts for the current fail-closed seam pending S37).
async function handleAddToWatchlist(product: ParsedProduct): Promise<ExtResponse> {
  const target = ownedTargets.resolve(product);
  const cred = await store.get<PairingCredential>(KEY_CREDENTIAL);
  if (!cred || !target) return { ok: true, watchlist: { ok: false, reason: "denied" } };
  const outcome = await watchlistGateway.addToWatchlist({
    marketplaceAccountId: cred.marketplaceAccountId,
    targetId: target.targetId,
  });
  incr("watchlist_add", { outcome: outcome.ok ? "accepted" : outcome.reason });
  return { ok: true, watchlist: outcome };
}

// EXT-005: overlay data for the product being viewed — resolved through the
// SAME Confirmed-owned-target gate (EXT-004), rendered, never recomputed. See
// overlay-read.ts for the current fail-closed seam (captureAuth is
// capture-only; a genuine, named contract-scope gap).
async function handleGetOverlayView(product: ParsedProduct): Promise<ExtResponse> {
  const target = ownedTargets.resolve(product);
  if (!target) return { ok: true, overlay: { kind: "unavailable" } };
  const result = await overlayReadGateway.fetchOverlayData(target.targetId);
  if (!result.ok) return { ok: true, overlay: { kind: "unavailable" } };
  const view = deriveOverlayView(result.target, result.offers, Date.now());
  return { ok: true, overlay: { kind: "ready", view } };
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
    await setCapability("ready");
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
  await store.remove(KEY_CREDENTIAL);
  await setCapability("revoked");
  log("info", "credential_cleared");
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
  };
}
