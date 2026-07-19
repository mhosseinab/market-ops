import type { ConnectorStatus } from "./types";

// Issue #18 — shared connector-health derivation. The TopBar pill (and any other
// summary surface) reads the CURRENT typed connector state through this single
// function so no view re-implements the rule. It binds directly to the generated
// ConnectorStatus schema (gen/ts), so a contract change surfaces here.
//
// FAIL CLOSED (PRD §4.6, ACC-001): only a connection that is `connected` AND has
// every §15.2 capability confirmed `supported` resolves to the positive health.
// Absent status, a `disconnected` connection, any not-yet-probed (`unknown`)
// capability, and any `degraded`/`unsupported` capability all resolve to a
// NON-positive health. Worst state wins so a degradation is never masked by an
// otherwise-healthy surface.

export type ConnectorHealth = "unknown" | "disconnected" | "probing" | "degraded" | "supported";

export function deriveConnectorHealth(status: ConnectorStatus | undefined | null): ConnectorHealth {
  // No status input at all → fail closed to the neutral, unavailable health.
  if (!status) return "unknown";

  // The connection itself must be current before any capability can read healthy.
  if (status.connectionState !== "connected") return "disconnected";

  const capabilities = status.capabilities ?? [];

  // Worst-wins precedence: a confirmed non-support (degraded/unsupported) beats a
  // pending probe, which in turn beats an all-supported set.
  if (capabilities.some((c) => c.status === "degraded" || c.status === "unsupported")) {
    return "degraded";
  }

  // Connected but not yet fully probed (or nothing to attest) is still in-flight,
  // never positive.
  if (capabilities.length === 0 || capabilities.some((c) => c.status === "unknown")) {
    return "probing";
  }

  return "supported";
}
