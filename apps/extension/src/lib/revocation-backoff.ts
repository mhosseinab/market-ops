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
//   - the marker's lifetime bound is the ATTEMPT COUNT, not the device clock
//     (see REVOCATION_MAX_ATTEMPTS); this only decides *when* the next attempt
//     may run.

export const REVOCATION_RETRY_BASE_MS = 60_000;
export const REVOCATION_RETRY_CEILING_MS = 60 * 60_000;
// Guards 2 ** exponent against a pathological persisted attempt count.
const MAX_EXPONENT = 20;

// The HARD lifetime bound on an unconfirmed revocation (issue #149, G1).
//
// A pending marker may not live forever: it keeps the credential material on the
// device and blocks re-pairing. But the bound must NOT be the device clock. The
// credential's `expiresAt` can only be judged against a clock the extension does
// not control, and a forward-skewed one would end the revoke early while the
// server row is live for its real remaining TTL — the exact #149 impact.
//
// The attempt COUNT is clock-independent: it only advances when a real request
// was made and came back non-authoritative. With the backoff above (1-minute
// base, 1-hour ceiling reached at attempt 7, jitter in [0.5, 1]) 48 attempts is
// at least ~20 hours and typically well over a day of genuine retrying before
// the extension gives up.
//
// Giving up terminates into `unknown` (not paired) — NEVER `revoked`. The
// authority never confirmed anything, so the popup must not claim a completed
// kill switch; the distinction is carried by the
// `credential_revocation{outcome:"abandoned_unconfirmed"}` metric.
export const REVOCATION_MAX_ATTEMPTS = 48;

// The DURABLE AGE BOUND on a pending revocation (issue #149, fix 3).
//
// The attempt budget above is clock-independent, which is its virtue and its
// gap: it only advances when a real request was MADE and came back
// non-authoritative. A device with ZERO server contact — offline, or one whose
// forced user retries correctly do not consume the authoritative budget — could
// therefore hold the pending marker forever, and the pending marker BLOCKS
// re-pairing. A user must never be locked out of the extension by an authority
// that never answered.
//
// So the marker also ages out, measured from the durable `requestedAt`. The
// resulting transition is INTO the "could not confirm" quarantine, which claims
// nothing about the server: the credential material is retained there and the
// revoke keeps retrying. That is why a clock-driven transition is safe HERE
// while a clock-driven terminal `revoked` remains forbidden (G1).
//
// 24 hours is chosen deliberately: it is comfortably longer than a transient
// outage, shorter than the ~31 hours the 48-attempt budget spans under the
// backoff above, and both bounds now converge on the SAME quarantine terminal —
// so whichever fires first, the outcome is identical and honest.
export const REVOCATION_PENDING_MAX_AGE_MS = 24 * 60 * 60_000;

// revocationPendingAgeExceeded reports whether the durable pending marker has
// outlived REVOCATION_PENDING_MAX_AGE_MS. It FAILS CLOSED on an absent or
// unparseable `requestedAt` — "not exceeded", i.e. keep retrying — so unreadable
// bookkeeping can never be the reason a revoke stops being pursued.
export function revocationPendingAgeExceeded(pending: PendingRevocation, nowMs: number): boolean {
  const requestedAt = Date.parse(pending.requestedAt ?? "");
  if (!Number.isFinite(requestedAt)) return false;
  return nowMs - requestedAt >= REVOCATION_PENDING_MAX_AGE_MS;
}

// revocationAttemptsExhausted reports whether the clock-independent attempt
// budget for an unconfirmed revocation is spent. It fails CLOSED on a
// non-finite/absent count (a marker written by an older or partially-written
// build): such a marker cannot be scheduled meaningfully, so it is treated as
// still having budget rather than being abandoned on the spot.
export function revocationAttemptsExhausted(attempts: number): boolean {
  if (!Number.isFinite(attempts)) return false;
  return attempts >= REVOCATION_MAX_ATTEMPTS;
}

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
