// Capture capability state machine (PRD §4.6: "Unknown never enables dependent
// UI or logic"; EXT-001/EXT-009). The capability starts UNKNOWN and only a
// successful pairing moves it to READY. Revocation (or a 401 on upload) moves it
// to REVOKED; the user disabling capture moves it to DISABLED. In every state
// except READY, capture and upload are a NO-OP — never a silent partial action.

// REVOCATION_PENDING (issue #149, PD-4(B)): the user asked to revoke and capture
// is already off, but the SERVER has not yet confirmed the credential is dead.
// It is deliberately DISTINCT from REVOKED: reporting a kill switch that has not
// actually invalidated authorization is exactly the bug #149 closes. Like every
// non-READY state it fails closed; unlike REVOKED it still holds the credential
// material, because the pending revoke has to be retried with it.
// REVOCATION_UNCONFIRMED (issue #149, fix 3): the explicit, terminal "could not
// confirm" QUARANTINE state. A revocation the authority never evidenced does not
// get silently discarded (which would defeat EXT-009 — the server row may still
// be live) and does not block re-pairing forever. Quarantine over inference
// (§4.6): the credential material is moved into a durable quarantine record that
// survives an MV3 restart, the revoke keeps retrying under the existing backoff,
// and the state is audited with its OWN metric outcome — never folded into
// `revoked` (a completed kill switch) or into a generic failure.
export type Capability =
  | "unknown"
  | "ready"
  | "revoked"
  | "disabled"
  | "revocation_pending"
  | "revocation_unconfirmed";

// The stable, LOCALE-NEUTRAL token for the quarantine state. Exported so the
// popup can render it without a string literal (copy-lint / LOC boundary) while
// its Persian copy is still pending product-owner approval (#149).
export const REVOCATION_UNCONFIRMED_TOKEN = "revocation_unconfirmed";

// captureEnabled reports whether passive capture + upload may proceed. It is the
// single gate the service worker consults before doing anything with page data.
// Only READY enables; UNKNOWN (never paired), REVOKED (credential killed), and
// DISABLED (user kill switch) all fail closed.
export function captureEnabled(capability: Capability): boolean {
  return capability === "ready";
}

// A human-readable, LOCALE-NEUTRAL degradation reason for the popup + logs. The
// popup maps this token to Persian copy through the locale pack; this string is
// a stable identifier, never user-facing copy (LOC boundary).
export function degradationReason(capability: Capability): string | null {
  switch (capability) {
    case "ready":
      return null;
    case "unknown":
      return "not_paired";
    case "revoked":
      return "credential_revoked";
    case "disabled":
      return "capture_disabled";
    case "revocation_pending":
      return "revocation_pending";
    case "revocation_unconfirmed":
      return REVOCATION_UNCONFIRMED_TOKEN;
  }
}
