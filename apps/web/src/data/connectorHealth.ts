import type { ConnectorCapability, ConnectorStatus } from "./types";

// Issue #18 — shared connector-health derivation. The TopBar pill (and any other
// summary surface) reads the CURRENT typed connector state through this single
// function so no view re-implements the rule. It binds directly to the generated
// ConnectorStatus schema (gen/ts), so a contract change surfaces here.
//
// FAIL CLOSED (PRD §4.6, ACC-001, §15.2): only a connection that is `connected`
// AND carries EXACTLY the nine §15.2 capabilities — each present, each unique,
// each confirmed `supported` — resolves to the positive health. Absent status, a
// `disconnected` connection, an INCOMPLETE set (a missing capability is
// unattested == still Unknown), a DUPLICATE set (a malformed, untrustworthy
// payload), any not-yet-probed (`unknown`) capability, and any
// `degraded`/`unsupported` capability all resolve to a NON-positive health.
// "Unknown never enables" (§15.2): the set is validated for COMPLETENESS and
// UNIQUENESS BEFORE health is derived, so a partial or corrupt attestation can
// never be read as healthy. Worst state wins so a real degradation is never
// masked by the completeness/uniqueness check.

export type ConnectorHealth = "unknown" | "disconnected" | "probing" | "degraded" | "supported";

// The nine §15.2 capabilities that MUST all be attested before health is positive.
// Bound to the generated ConnectorCapability enum by the compile-time
// exhaustiveness assertion below, so a §15.2 contract change fails typecheck here
// rather than silently narrowing the completeness gate.
export const EXPECTED_CAPABILITIES = [
  "catalog_read",
  "owned_offer_read",
  "stock_read",
  "buybox_read",
  "boundary_read",
  "commission_read",
  "sales_context_read",
  "price_write",
  "change_feed",
] as const;

// Compile-time proof that EXPECTED_CAPABILITIES is EXACTLY the ConnectorCapability
// union (both directions). If the contract adds/removes/renames a capability, one
// of these assignments stops type-checking — the gate can never drift silently.
type ExpectedCapability = (typeof EXPECTED_CAPABILITIES)[number];
const _expectedCoversContract: ConnectorCapability = "" as ExpectedCapability;
const _contractCoversExpected: ExpectedCapability = "" as ConnectorCapability;
void _expectedCoversContract;
void _contractCoversExpected;

export function deriveConnectorHealth(status: ConnectorStatus | undefined | null): ConnectorHealth {
  // No status input at all → fail closed to the neutral, unavailable health.
  if (!status) return "unknown";

  // The connection itself must be current before any capability can read healthy.
  if (status.connectionState !== "connected") return "disconnected";

  const capabilities = status.capabilities ?? [];

  // Worst-wins precedence: a confirmed non-support (degraded/unsupported) is the
  // most severe signal and must surface even from a malformed/incomplete set, so
  // a real degradation is never masked by the validation gates below.
  if (capabilities.some((c) => c.status === "degraded" || c.status === "unsupported")) {
    return "degraded";
  }

  // UNIQUENESS gate: a capability reported more than once is a corrupt, untrustworthy
  // attestation. We cannot determine health from it → fail closed to the neutral
  // Unknown state (never `supported`).
  const names = capabilities.map((c) => c.capability);
  if (new Set(names).size !== names.length) return "unknown";

  // COMPLETENESS gate: every §15.2 capability must be present, and any capability
  // still `unknown` is not yet probed. A missing capability is unattested — treated
  // exactly like a not-yet-probed one. Either way the connector is in-flight, never
  // positive. (An empty set is the fully-incomplete case → probing.)
  const present = new Set(names);
  const complete = EXPECTED_CAPABILITIES.every((expected) => present.has(expected));
  if (!complete || capabilities.some((c) => c.status === "unknown")) return "probing";

  // Connected, exactly the nine §15.2 capabilities, each unique and confirmed
  // supported — the only path to the positive health.
  return "supported";
}
