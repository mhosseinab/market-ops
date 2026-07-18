// Capture capability state machine (PRD §4.6: "Unknown never enables dependent
// UI or logic"; EXT-001/EXT-009). The capability starts UNKNOWN and only a
// successful pairing moves it to READY. Revocation (or a 401 on upload) moves it
// to REVOKED; the user disabling capture moves it to DISABLED. In every state
// except READY, capture and upload are a NO-OP — never a silent partial action.

export type Capability = "unknown" | "ready" | "revoked" | "disabled";

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
  }
}
