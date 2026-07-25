import { describe, expect, it } from "vitest";
import {
  auditNoSellerToken,
  KEY_CREDENTIAL,
  KEY_QUEUE,
  KEY_REVOCATION_ABANDONED,
  KEY_REVOCATION_UNCONFIRMED,
  MemoryStore,
  sanitizeCredential,
} from "./storage";
import type { PairingCredential } from "./types";

describe("storage audit — EXT-001: ONLY a capture credential, NEVER a seller token", () => {
  it("sanitizeCredential persists ONLY the allow-listed capture-credential fields", () => {
    // A hostile/overzealous claim response that also smuggles a seller token.
    const claim = {
      credential: "cap-cred-hex",
      credentialId: "33333333-3333-3333-3333-333333333333",
      marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
      expiresAt: "2026-08-01T00:00:00Z",
      sellerApiToken: "FAKE-seller-token-should-be-stripped",
      dkOpenApiKey: "FAKE-dk-key-should-be-stripped",
    } as unknown as PairingCredential;

    const stored = sanitizeCredential(claim);
    const keys = Object.keys(stored);
    expect(keys.sort()).toEqual([
      "credential",
      "credentialId",
      "expiresAt",
      "marketplaceAccountId",
    ]);
    expect(JSON.stringify(stored)).not.toContain("FAKE-seller-token-should-be-stripped");
    expect(JSON.stringify(stored)).not.toContain("FAKE-dk-key-should-be-stripped");
  });

  it("auditNoSellerToken finds NO seller-token-shaped secret in a real storage snapshot", async () => {
    const store = new MemoryStore();
    await store.set(
      KEY_CREDENTIAL,
      sanitizeCredential({
        credential: "cap-cred-hex",
        credentialId: "33333333-3333-3333-3333-333333333333",
        marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
        expiresAt: "2026-08-01T00:00:00Z",
      }),
    );
    await store.set(KEY_QUEUE, []);
    const offenders = auditNoSellerToken(await store.snapshot());
    expect(offenders).toEqual([]);
  });

  // Issue #149 (F4 fold-in). `expiresAt` is `required` in the contract, but
  // sanitizeCredential only FILTERED keys — it never validated them. A malformed
  // or absent value therefore yielded Number.isFinite(NaN) === false at the
  // pending-revocation expiry check, which silently disabled the ONLY bound on a
  // pending marker's lifetime. Validate at the storage boundary (typed/validated
  // boundaries, CLAUDE.md) so an unusable credential is never persisted at all.
  it("sanitizeCredential REJECTS a credential whose expiresAt is missing or unparseable", () => {
    const base = {
      credential: "cap-cred-hex",
      credentialId: "33333333-3333-3333-3333-333333333333",
      marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
      expiresAt: "2026-08-01T00:00:00Z",
    };
    for (const bad of [
      { ...base, expiresAt: "not-a-timestamp" },
      { ...base, expiresAt: "" },
      { ...base, expiresAt: undefined },
      { ...base, expiresAt: 1234567890 },
      { ...base, credential: "" },
      { ...base, credentialId: undefined },
      { ...base, marketplaceAccountId: "" },
    ]) {
      expect(() => sanitizeCredential(bad as unknown as PairingCredential)).toThrow();
    }
    // …and a well-formed one still round-trips.
    expect(sanitizeCredential(base as PairingCredential).expiresAt).toBe(base.expiresAt);
  });

  it("auditNoSellerToken FAILS CLOSED if a seller token ever lands in storage", () => {
    const snapshot = {
      credential: { credential: "ok", sellerToken: "leak" },
      other: { dk_api_secret: "x" },
    };
    const offenders = auditNoSellerToken(snapshot);
    expect(offenders.length).toBeGreaterThan(0);
  });
});

// Issue #149, fix 3: the "could not confirm" QUARANTINE record is a NEW place
// capture-credential material lives. A new home for a secret must not escape the
// storage audit — otherwise EXT-001's allow-list check would simply stop
// covering the credential the moment it moved.
describe("storage audit — the unconfirmed-revocation quarantine record (#149)", () => {
  it("its key name is not seller-token-shaped, and a clean record has NO offenders", () => {
    const snapshot = {
      [KEY_REVOCATION_UNCONFIRMED]: {
        credential: "cap-cred-hex",
        credentialId: "33333333-3333-3333-3333-333333333333",
        marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
        credentialExpiresAt: "2026-08-01T00:00:00Z",
        requestedAt: "2026-07-25T00:00:00Z",
        attempts: 48,
        evidence: "unconfirmed_generic_401",
        nextAttemptAt: "2026-07-25T01:00:00Z",
      },
    };
    expect(auditNoSellerToken(snapshot)).toEqual([]);
  });

  it("FAILS CLOSED on a NON-allow-listed field in the quarantine record", () => {
    const offenders = auditNoSellerToken({
      [KEY_REVOCATION_UNCONFIRMED]: {
        credential: "cap-cred-hex",
        credentialId: "33333333-3333-3333-3333-333333333333",
        marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
        credentialExpiresAt: "2026-08-01T00:00:00Z",
        requestedAt: "2026-07-25T00:00:00Z",
        attempts: 1,
        evidence: "unconfirmed_transport",
        // Not allow-listed — page-derived/session-adjacent data must never ride
        // along in the quarantine record (docs/12).
        lastSeenUrl: "https://www.digikala.com/product/dkp-1/",
      },
    });
    expect(offenders.length).toBeGreaterThan(0);
    expect(offenders.join(" ")).toContain("lastSeenUrl");
  });

  it("FAILS CLOSED on a seller-token-shaped key smuggled into the quarantine record", () => {
    const offenders = auditNoSellerToken({
      [KEY_REVOCATION_UNCONFIRMED]: { credential: "ok", seller_token: "leak" },
    });
    expect(offenders.length).toBeGreaterThan(0);
  });

  // B2 (fix cycle 1): the quarantine store is a BOUNDED LIST, so the audit must
  // cover EVERY entry. Auditing only the first would let a secret's new home
  // escape simply by being the second outstanding revocation.
  it("audits EVERY entry of the bounded quarantine LIST, not just the first", () => {
    const clean = {
      credential: "cap-cred-hex",
      credentialId: "33333333-3333-3333-3333-333333333333",
      marketplaceAccountId: "11111111-1111-1111-1111-111111111111",
      credentialExpiresAt: "2026-08-01T00:00:00Z",
      requestedAt: "2026-07-25T00:00:00Z",
      attempts: 48,
      evidence: "unconfirmed_generic_401",
      nextAttemptAt: "2026-07-25T01:00:00Z",
    };
    expect(auditNoSellerToken({ [KEY_REVOCATION_UNCONFIRMED]: [clean, clean] })).toEqual([]);

    const offenders = auditNoSellerToken({
      [KEY_REVOCATION_UNCONFIRMED]: [clean, { ...clean, lastSeenUrl: "https://x/", jwt: "leak" }],
    });
    expect(offenders.join(" ")).toContain("lastSeenUrl");
    expect(offenders.join(" ")).toContain("[1]");
    expect(offenders.join(" ")).toContain("jwt");
  });

  // Fix cycle 3 (upheld). The durable abandoned flag is a BARE BOOLEAN by
  // design — the eviction's whole point is that the material is gone. Nothing
  // pinned that shape, so a future change writing a record there would have been
  // caught only by the coarse seller-token-key regex, which does not match
  // `credential`. The audit fails closed on any non-boolean at that key.
  it("pins the abandoned flag's SHAPE: a bare boolean passes, anything else fails closed", () => {
    expect(auditNoSellerToken({ [KEY_REVOCATION_ABANDONED]: true })).toEqual([]);
    expect(auditNoSellerToken({ [KEY_REVOCATION_ABANDONED]: false })).toEqual([]);
    for (const bad of [
      { credential: "cap-cred-hex", credentialId: "33333333-3333-3333-3333-333333333333" },
      ["cap-cred-hex"],
      "cap-cred-hex",
    ]) {
      const offenders = auditNoSellerToken({ [KEY_REVOCATION_ABANDONED]: bad });
      expect(offenders.length).toBeGreaterThan(0);
      expect(offenders.join(" ")).toContain(KEY_REVOCATION_ABANDONED);
    }
  });
});
