import type { CaptureUpload } from "./types";

// Idempotency (docs/09: "compute PII-redacted content hashes for idempotency so
// the backend can dedupe a replayed batch by content hash"; PRD event/observation
// dedup invariant). The dedup key is derived from ONLY the identity-bearing,
// already-redacted capture fields — the SAME inputs the server folds on (OBS-008
// DedupKey): target, offer identity, route, raw price/status, and capture
// instant. A byte-identical offline replay therefore produces an identical key,
// so the server creates NO duplicate current offer.
//
// The key carries no PII by construction (it is built from allow-listed fields
// only) and is stable across process restarts (pure function of the payload).

export function dedupKey(c: CaptureUpload): string {
  const parts = [
    c.targetId,
    String(c.nativeVariantId),
    c.subRoute,
    c.price?.value?.trim() ?? "",
    c.price?.unit?.trim() ?? "",
    c.listPrice?.value?.trim() ?? "",
    c.availabilityStatus,
    c.capturedAt,
  ];
  return fnv1a(parts.join(""));
}

// fnv1a is a fast, deterministic 32-bit content hash rendered as fixed-width hex.
// It is used only as an idempotency key (not a security primitive).
function fnv1a(input: string): string {
  let hash = 0x811c9dc5;
  for (let i = 0; i < input.length; i++) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, 0x01000193);
  }
  // >>> 0 forces an unsigned 32-bit result before hex rendering.
  return (hash >>> 0).toString(16).padStart(8, "0");
}
