import type { Capability } from "./capability";
import type { CaptureUpload, PairingCredential } from "./types";

// A minimal async key/value surface so the queue + credential store are testable
// without chrome. The chrome adapter (chromeLocalStore) wraps
// chrome.storage.local; MemoryStore backs the unit tests.
export interface KeyValueStore {
  get<T>(key: string): Promise<T | undefined>;
  set<T>(key: string, value: T): Promise<void>;
  remove(key: string): Promise<void>;
  snapshot(): Promise<Record<string, unknown>>;
}

export class MemoryStore implements KeyValueStore {
  private data = new Map<string, unknown>();
  async get<T>(key: string): Promise<T | undefined> {
    return this.data.has(key) ? (this.data.get(key) as T) : undefined;
  }
  async set<T>(key: string, value: T): Promise<void> {
    // Store a structured clone so callers cannot mutate persisted state by ref.
    this.data.set(key, JSON.parse(JSON.stringify(value)));
  }
  async remove(key: string): Promise<void> {
    this.data.delete(key);
  }
  async snapshot(): Promise<Record<string, unknown>> {
    return Object.fromEntries(this.data.entries());
  }
}

// chromeLocalStore wraps chrome.storage.local (docs/09: queue/config live in
// chrome.storage.local). Instantiated ONLY inside the service worker.
export function chromeLocalStore(): KeyValueStore {
  return {
    async get<T>(key: string): Promise<T | undefined> {
      const out = await chrome.storage.local.get(key);
      return out[key] as T | undefined;
    },
    async set<T>(key: string, value: T): Promise<void> {
      await chrome.storage.local.set({ [key]: value });
    },
    async remove(key: string): Promise<void> {
      await chrome.storage.local.remove(key);
    },
    async snapshot(): Promise<Record<string, unknown>> {
      return await chrome.storage.local.get(null);
    },
  };
}

// Storage keys. Everything the extension persists is listed here; the storage
// audit walks the whole snapshot to prove nothing else (and nothing token-like)
// ever lands.
export const KEY_CAPABILITY = "capability";
export const KEY_CREDENTIAL = "credential";
export const KEY_QUEUE = "queue";
// Durable dead-letter store (issue #150): exhausted transient failures are moved
// HERE, never deleted as if accepted. A separate key from KEY_QUEUE so pending
// delivery and the operator-recoverable failure record are inspected + mutated
// independently, and so a dead-letter item never re-enters an automatic flush.
export const KEY_DEADLETTER = "deadLetter";
export const KEY_LAST_UPLOAD = "lastUploadAt";
// Durable pending-revocation marker (issue #149 / EXT-009). Written BEFORE the
// server revoke is attempted and cleared only once the authority confirms (or an
// authoritative expiry is reached), so an MV3 worker teardown mid-revoke can
// never lose the revocation. It holds ONLY non-secret bookkeeping — the
// credential material stays in KEY_CREDENTIAL (one store for the secret), which
// is exactly why a failed revoke must NOT clear that key.
export const KEY_REVOCATION_PENDING = "revocationPending";
// Durable operational-telemetry outbox (issue #162): bounded, allow-listed metric
// snapshots that must survive an MV3 worker restart and be exported to an
// operational sink. Persisted here so the storage audit walks it too — a batch
// carries only bounded parser/version/status labels + counts, never PII, URLs, or
// raw marketplace text (the outbox sanitizes on write).
export const KEY_TELEMETRY_OUTBOX = "telemetryOutbox";

// The ONLY fields of a stored capture credential. EXT-001: the extension holds a
// capture/overlay credential and NEVER a seller-API token — so the persisted
// record is exactly the pairing-claim result, nothing more.
const ALLOWED_CREDENTIAL_KEYS = new Set([
  "credential",
  "credentialId",
  "marketplaceAccountId",
  "expiresAt",
]);

// Field-name shapes that would indicate a seller-API / long-lived token slipped
// into storage. The audit fails closed on any of these.
const SELLER_TOKEN_KEY_RE =
  /(seller|dk|open[_-]?api|access|refresh|bearer|api)[_-]?(token|key|secret|credential)|jwt|password|cookie|session/i;

// sanitizeCredential returns a credential record containing ONLY the allow-listed
// capture-credential fields. Any extra field (e.g. an accidentally-included
// seller token) is dropped before it can be persisted (fail closed).
//
// It also VALIDATES those fields (issue #149). Filtering keys alone was not
// enough: every allow-listed field is `required` in the gateway contract, but a
// malformed `expiresAt` used to persist happily and then yield
// Number.isFinite(NaN) === false at the pending-revocation expiry check —
// silently disabling the ONLY bound on a pending marker's lifetime. A credential
// that cannot support the kill switch is not storable at all; the caller
// surfaces this as a failed pairing rather than pairing into an unusable state.
export function sanitizeCredential(cred: PairingCredential): PairingCredential {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(cred ?? {})) {
    if (ALLOWED_CREDENTIAL_KEYS.has(k)) out[k] = v;
  }
  for (const key of ALLOWED_CREDENTIAL_KEYS) {
    const v = out[key];
    if (typeof v !== "string" || v === "") {
      throw new Error(`invalid capture credential: ${key} is missing or not a non-empty string`);
    }
  }
  if (!Number.isFinite(Date.parse(out.expiresAt as string))) {
    throw new Error("invalid capture credential: expiresAt is not a parseable instant");
  }
  return out as unknown as PairingCredential;
}

// auditNoSellerToken walks a full storage snapshot and reports every offending
// path where a seller-token-shaped key appears. An empty result means the store
// holds only a capture credential + queue/config (EXT-001 satisfied).
export function auditNoSellerToken(snapshot: Record<string, unknown>): string[] {
  const offenders: string[] = [];
  walk(snapshot, "", offenders);
  // The stored credential must additionally carry ONLY the allow-listed keys.
  const cred = snapshot[KEY_CREDENTIAL];
  if (cred && typeof cred === "object") {
    for (const key of Object.keys(cred as object)) {
      if (!ALLOWED_CREDENTIAL_KEYS.has(key)) {
        offenders.push(`${KEY_CREDENTIAL}.${key} (not an allow-listed capture-credential field)`);
      }
    }
  }
  return offenders;
}

function walk(value: unknown, path: string, offenders: string[]): void {
  if (Array.isArray(value)) {
    value.forEach((v, i) => {
      walk(v, `${path}[${i}]`, offenders);
    });
    return;
  }
  if (value !== null && typeof value === "object") {
    for (const [key, v] of Object.entries(value as Record<string, unknown>)) {
      const here = path ? `${path}.${key}` : key;
      if (SELLER_TOKEN_KEY_RE.test(key)) offenders.push(`${here} (seller-token-shaped key)`);
      walk(v, here, offenders);
    }
  }
}

// A revocation the user requested that the SERVER has not yet confirmed (issue
// #149). JSON-safe so it survives an MV3 worker restart byte-identically. It
// carries NO credential secret: only the credential's identity (for correlating
// a retry with the still-stored credential) and its authoritative expiry, after
// which the credential is dead at the server regardless and the material may be
// discarded.
export interface PendingRevocation {
  requestedAt: string;
  credentialId: string;
  marketplaceAccountId: string;
  // The credential's server-side expiry (from the pairing claim). At/after this
  // instant the credential cannot authenticate anything, so the pending marker
  // resolves without a further server round-trip.
  credentialExpiresAt: string;
  attempts: number;
  // The earliest instant the NEXT retry may run (issue #149). Derived from
  // `attempts` by an exponential, credential-jittered, ceiling-capped backoff
  // (see revocation-backoff.ts) and persisted so an MV3 teardown cannot reset
  // the schedule back to one request per alarm tick. OPTIONAL: a marker written
  // by an older build has none and is treated as due immediately — a pending
  // revoke is never stranded because its schedule is missing.
  nextAttemptAt?: string;
  // Whether the SERVER has ever answered this pending revoke with a real HTTP
  // response (issue #149). The credential's expiry can only be judged against
  // the DEVICE clock, so a clock skewed forward would otherwise "expire" a live
  // credential and report the kill switch complete. An extension that has never
  // reached the gateway has no evidence its clock is right, so it may not take
  // the expiry shortcut. OPTIONAL for the same durability reason as above.
  serverContacted?: boolean;
}

// A queued upload item: the allow-listed capture, its stable dedup key, and the
// retry bookkeeping. Persisted verbatim so an offline replay after a browser
// restart is byte-identical (idempotent).
export interface QueuedItem {
  dedupKey: string;
  capture: CaptureUpload;
  attempts: number;
  enqueuedAt: string;
}

// The reason an item was dead-lettered. A stable, LOCALE-NEUTRAL token (never
// display copy) — the popup maps it to catalog copy. Additive within the schema
// major; a new reason is a new token, never a reworded existing one.
export type DeadLetterReason = "max_attempts_exhausted";

// A durably preserved delivery that exhausted its bounded retry budget (issue
// #150). It is NOT accepted evidence and NOT a permanent client rejection — it
// is retained, inspectable, and operator-recoverable. JSON-safe so it persists
// byte-identically across an MV3 worker restart, and it keeps the ORIGINAL
// dedupKey so a later retry stays server-idempotent (crash-after-upload replay).
export interface DeadLetterItem {
  dedupKey: string;
  capture: CaptureUpload;
  attempts: number;
  enqueuedAt: string;
  deadLetteredAt: string;
  failureReason: DeadLetterReason;
}

// A compact, popup-facing view of one dead-letter item: the stable id needed to
// address a retry/discard action + its locale-neutral reason token. Carries no
// PII and no marketplace free text (the capture is allow-listed by construction).
export interface DeadLetterSummary {
  dedupKey: string;
  failureReason: DeadLetterReason;
}

// The popup-facing kill-switch/degradation snapshot (EXT-009). Persisted-derived,
// never a silent no-op: disabling produces a visibly disabled state.
export interface PopupState {
  capability: Capability;
  marketplaceAccountId: string | null;
  lastUploadAt: string | null;
  queuedCount: number;
  degradation: string | null;
  // EXT-012 opt-in toggle for bounded scheduled refresh (server-allocated).
  scheduleEnabled: boolean;
  // Durable dead-letter (issue #150): exhausted deliveries the operator can
  // retry or discard. A non-empty list is a VISIBLE, real degradation surface
  // (EXT-009) — never a silent drop.
  deadLetter: DeadLetterSummary[];
}
