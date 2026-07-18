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
export const KEY_LAST_UPLOAD = "lastUploadAt";

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
export function sanitizeCredential(cred: PairingCredential): PairingCredential {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(cred)) {
    if (ALLOWED_CREDENTIAL_KEYS.has(k)) out[k] = v;
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

// A queued upload item: the allow-listed capture, its stable dedup key, and the
// retry bookkeeping. Persisted verbatim so an offline replay after a browser
// restart is byte-identical (idempotent).
export interface QueuedItem {
  dedupKey: string;
  capture: CaptureUpload;
  attempts: number;
  enqueuedAt: string;
}

// The popup-facing kill-switch/degradation snapshot (EXT-009). Persisted-derived,
// never a silent no-op: disabling produces a visibly disabled state.
export interface PopupState {
  capability: Capability;
  marketplaceAccountId: string | null;
  lastUploadAt: string | null;
  queuedCount: number;
  degradation: string | null;
}
