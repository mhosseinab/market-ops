import type { PendingRevocation } from "./storage";

// Retry schedule for an unconfirmed capture-credential revocation (issue #149).
//
// The pending revoke is driven by the 1-minute flush alarm. Without a schedule
// that meant one gateway request per minute, per extension, for as long as the
// revoke stayed unconfirmed — up to the credential's 30-day TTL (~43,200
// requests each) — with every extension in the fleet ticking in lockstep.
// CLAUDE.md is explicit: backpressure is the default, queues do not grow
// unbounded, and rate limiting sits in front of every external call.
//
// The schedule is a PURE function of state the marker already persists, so it
// survives an MV3 teardown byte-identically and is deterministic under test:
//   - exponential growth from a 1-minute base, capped at a 1-hour ceiling;
//   - jitter derived from the CREDENTIAL ID (not Math.random), which is both
//     reproducible and genuinely de-synchronising — two extensions hold
//     different credential ids and therefore never retry on the same schedule;
//   - the credential's authoritative expiry remains the HARD bound (settled in
//     the service worker); this only decides *when* the next attempt may run.

export const REVOCATION_RETRY_BASE_MS = 60_000;
export const REVOCATION_RETRY_CEILING_MS = 60 * 60_000;
// Guards 2 ** exponent against a pathological persisted attempt count.
const MAX_EXPONENT = 20;

// hashFraction maps a string to a stable fraction in [0, 1) via FNV-1a. It is
// used ONLY to spread retry timing; it is never an identifier and never logged.
function hashFraction(seed: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h / 0x100000000;
}

// revocationRetryDelayMs is the wait AFTER `attempts` failed attempts. The
// jittered result is in [delay/2, delay] and can never exceed the ceiling, so
// the fleet spreads out without any extension drifting past the cap.
export function revocationRetryDelayMs(attempts: number, credentialId: string): number {
  const exponent = Math.min(Math.max(attempts, 1) - 1, MAX_EXPONENT);
  const capped = Math.min(REVOCATION_RETRY_BASE_MS * 2 ** exponent, REVOCATION_RETRY_CEILING_MS);
  const jitter = 0.5 + 0.5 * hashFraction(`${credentialId}:${attempts}`);
  return Math.max(1, Math.round(capped * jitter));
}

// nextRevocationAttemptAt is the ISO instant the NEXT retry becomes due, stored
// on the durable marker so a worker teardown cannot reset the backoff.
export function nextRevocationAttemptAt(
  attempts: number,
  credentialId: string,
  nowMs: number,
): string {
  return new Date(nowMs + revocationRetryDelayMs(attempts, credentialId)).toISOString();
}

// revocationRetryDue reports whether the scheduled instant has arrived. It fails
// OPEN (due) for a marker with no schedule or an unparseable one — a marker
// written by an older build, or a partially-written one, must never be stranded
// un-retried, which would leave a live credential in the wild.
export function revocationRetryDue(pending: PendingRevocation, nowMs: number): boolean {
  const due = Date.parse(pending.nextAttemptAt ?? "");
  if (!Number.isFinite(due)) return true;
  return nowMs >= due;
}
