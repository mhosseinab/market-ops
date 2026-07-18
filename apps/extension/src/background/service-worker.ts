import { type Capability, degradationReason } from "../lib/capability";
import { GatewayClient } from "../lib/gateway";
import type { ExtMessage, ExtResponse } from "../lib/messages";
import { incr, log } from "../lib/observability";
import { OwnedTargetIndex } from "../lib/owned-targets";
import { prepareCapture } from "../lib/pipeline";
import { UploadQueue } from "../lib/queue";
import { initDevErrorReporting } from "../lib/spotlight";
import {
  chromeLocalStore,
  KEY_CAPABILITY,
  KEY_CREDENTIAL,
  KEY_LAST_UPLOAD,
  type PopupState,
  sanitizeCredential,
} from "../lib/storage";
import type { PairingCredential } from "../lib/types";

// The MV3 service worker owns pairing, queueing, and delivery discipline (docs/09).
// It holds ONLY a capture credential (EXT-001), gates every capture on capability
// + Confirmed ownership, and flushes the offline queue with bounded backoff.
// Alarms are scheduling HINTS for queue flush — never autonomous crawling.

const GATEWAY_BASE_URL = import.meta.env.VITE_GATEWAY_BASE_URL ?? "http://localhost:8080";
const FLUSH_ALARM = "queue-flush";

const store = chromeLocalStore();
const queue = new UploadQueue(store);
const gateway = new GatewayClient(GATEWAY_BASE_URL);
// Confirmed owned targets: server-authoritative, starts EMPTY (fail closed —
// EXT-004). Populated by the S31 authenticated targets sync.
const ownedTargets = new OwnedTargetIndex();

void initDevErrorReporting("service-worker");

chrome.runtime.onInstalled.addListener(() => {
  chrome.alarms.create(FLUSH_ALARM, { periodInMinutes: 1 });
});
chrome.alarms.onAlarm.addListener((a) => {
  if (a.name === FLUSH_ALARM) void flush();
});

chrome.runtime.onMessage.addListener((msg: ExtMessage, _sender, sendResponse) => {
  void handle(msg).then(sendResponse);
  return true; // async response
});

async function handle(msg: ExtMessage): Promise<ExtResponse> {
  switch (msg.kind) {
    case "capture":
      return handleCapture(msg);
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

async function handleCapture(msg: Extract<ExtMessage, { kind: "capture" }>): Promise<ExtResponse> {
  const capability = await getCapability();
  const decision = prepareCapture(msg.product, ownedTargets, capability, new Date().toISOString());
  if (decision.action === "skip") {
    log("info", "capture_skipped", { reason: decision.reason });
    return { ok: true, state: await popupState() };
  }
  const r = await queue.enqueue(decision.capture);
  if (r.shed) incr("queue_backpressure");
  incr("queue_depth", {}, 0);
  await flush();
  return { ok: true, state: await popupState() };
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
  incr("queue_depth", {}, 0);
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
  };
}
