import { describe, expect, it } from "vitest";
import {
  nextRevocationAttemptAt,
  REVOCATION_RETRY_CEILING_MS,
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
