import { describe, expect, it } from "vitest";
import {
  nextRevocationAttemptAt,
  REVOCATION_PENDING_MAX_AGE_MS,
  REVOCATION_RETRY_CEILING_MS,
  revocationPendingAgeExceeded,
  revocationRetryDelayMs,
  revocationRetryDue,
} from "./revocation-backoff";
import type { PendingRevocation } from "./storage";

// Issue #149 (F3). The pending-revoke retry originally ran on every 1-minute
// flush alarm with no backoff and no ceiling: a gateway outage against a 30-day
// credential TTL yields ~43,200 revoke requests per extension, from every
// extension in lockstep. CLAUDE.md is explicit that backpressure is the default,
// that queues do not grow unbounded, and that rate limiting sits in front of
// every external call.
//
// The schedule is a PURE function so it is deterministic under test: the jitter
// is derived from the credential id (which also de-synchronises the fleet — two
// extensions never retry on the same schedule) rather than from Math.random().
const CRED_A = "33333333-3333-3333-3333-333333333333";
const CRED_B = "44444444-4444-4444-4444-444444444444";

function pending(over: Partial<PendingRevocation> = {}): PendingRevocation {
  return {
    requestedAt: "2026-07-01T00:00:00.000Z",
    credentialId: CRED_A,
    marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
    credentialExpiresAt: "2026-08-01T00:00:00.000Z",
    attempts: 1,
    ...over,
  };
}

describe("revocation retry backoff (#149 F3) — bounded, jittered, capped", () => {
  it("grows exponentially and NEVER exceeds the ceiling, however many attempts", () => {
    let previous = 0;
    for (let attempts = 1; attempts <= 8; attempts++) {
      const delay = revocationRetryDelayMs(attempts, CRED_A);
      expect(delay).toBeGreaterThan(0);
      expect(delay).toBeLessThanOrEqual(REVOCATION_RETRY_CEILING_MS);
      previous = delay;
    }
    expect(previous).toBeGreaterThan(revocationRetryDelayMs(1, CRED_A));
    // A pathological attempt count (a month of retries) still cannot exceed the
    // ceiling or overflow into a non-finite delay.
    for (const attempts of [50, 1000, 100000]) {
      const delay = revocationRetryDelayMs(attempts, CRED_A);
      expect(Number.isFinite(delay)).toBe(true);
      expect(delay).toBeLessThanOrEqual(REVOCATION_RETRY_CEILING_MS);
    }
  });

  it("is deterministic per credential but DIFFERENT across credentials — no fleet lockstep", () => {
    expect(revocationRetryDelayMs(3, CRED_A)).toBe(revocationRetryDelayMs(3, CRED_A));
    const spread = new Set<number>();
    for (let i = 0; i < 32; i++) spread.add(revocationRetryDelayMs(3, `cred-${i}`));
    // If every extension computed the same delay this set would have size 1 —
    // that is exactly the lockstep thundering herd the finding describes.
    expect(spread.size).toBeGreaterThan(4);
    expect(revocationRetryDelayMs(3, CRED_A)).not.toBe(revocationRetryDelayMs(3, CRED_B));
  });

  it("bounds a 30-day outage to orders of magnitude fewer requests than 1/min", () => {
    // Walk the real schedule across the full credential TTL and count attempts.
    const ttlMs = 30 * 24 * 60 * 60 * 1000;
    let elapsed = 0;
    let attempts = 0;
    while (elapsed < ttlMs) {
      attempts++;
      elapsed += revocationRetryDelayMs(attempts, CRED_A);
    }
    expect(attempts).toBeLessThan(2000); // vs. 43,200 at a fixed 1/min
  });

  it("defers a retry until its scheduled instant, then allows exactly one", () => {
    const now = Date.parse("2026-07-01T00:10:00.000Z");
    const due = pending({ nextAttemptAt: new Date(now + 60_000).toISOString() });
    expect(revocationRetryDue(due, now)).toBe(false);
    expect(revocationRetryDue(due, now + 60_000)).toBe(true);
    expect(revocationRetryDue(due, now + 120_000)).toBe(true);
  });

  it("treats a marker with NO schedule (first attempt, or an older build) as due now", () => {
    // Durability across versions: a marker persisted before this field existed
    // must never be stranded un-retried.
    expect(revocationRetryDue(pending({ attempts: 0 }), Date.now())).toBe(true);
    expect(revocationRetryDue(pending({ nextAttemptAt: "not-a-date" }), Date.now())).toBe(true);
  });

  it("nextRevocationAttemptAt returns an ISO instant strictly in the future", () => {
    const now = Date.parse("2026-07-01T00:00:00.000Z");
    const next = nextRevocationAttemptAt(1, CRED_A, now);
    expect(Date.parse(next)).toBeGreaterThan(now);
    expect(new Date(next).toISOString()).toBe(next); // JSON-safe, canonical
  });
});

// Issue #149, fix 3: the pending marker needs a DURABLE AGE BOUND as well as an
// attempt budget. The attempt budget only advances when a request was actually
// made and came back non-authoritative, so a device with ZERO server contact
// (offline, or a forced user retry that must not consume the authoritative
// budget) could hold the marker — and therefore block re-pairing — indefinitely.
//
// A clock-driven transition INTO the "could not confirm" quarantine is SAFE and
// permitted: it never claims a revocation happened. What the device clock may
// never produce is a terminal `revoked` (G1) — that invariant is untouched here.
describe("pending-revocation AGE bound (#149 fix 3) — bounded even with zero server contact", () => {
  const requestedAt = "2026-07-01T00:00:00.000Z";
  const t0 = Date.parse(requestedAt);

  it("is NOT exceeded inside the window, and IS exceeded once past it", () => {
    const p = pending({ requestedAt, attempts: 0 });
    expect(revocationPendingAgeExceeded(p, t0)).toBe(false);
    expect(revocationPendingAgeExceeded(p, t0 + REVOCATION_PENDING_MAX_AGE_MS - 1)).toBe(false);
    expect(revocationPendingAgeExceeded(p, t0 + REVOCATION_PENDING_MAX_AGE_MS)).toBe(true);
    expect(revocationPendingAgeExceeded(p, t0 + 10 * REVOCATION_PENDING_MAX_AGE_MS)).toBe(true);
  });

  it("the bound is finite and durable-record-derived (requestedAt), not attempt-derived", () => {
    expect(Number.isFinite(REVOCATION_PENDING_MAX_AGE_MS)).toBe(true);
    expect(REVOCATION_PENDING_MAX_AGE_MS).toBeGreaterThan(0);
    // Zero attempts ever made — the attempt budget can never fire here, which is
    // exactly the case this bound exists for.
    expect(revocationPendingAgeExceeded(pending({ requestedAt, attempts: 0 }), t0 + 1)).toBe(false);
  });

  it("FAILS CLOSED on an absent/unparseable requestedAt — never abandons a marker on bad data", () => {
    // Fail closed here means "keep retrying", i.e. NOT exceeded: the marker
    // stays live rather than being quarantined on unreadable bookkeeping.
    for (const bad of ["", "not-a-date", undefined as unknown as string]) {
      expect(revocationPendingAgeExceeded(pending({ requestedAt: bad }), Date.now())).toBe(false);
    }
  });

  it("a clock skewed BACKWARD never trips the bound", () => {
    expect(revocationPendingAgeExceeded(pending({ requestedAt }), t0 - 1_000_000)).toBe(false);
  });
});
