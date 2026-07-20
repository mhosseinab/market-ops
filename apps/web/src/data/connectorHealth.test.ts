import { describe, expect, it } from "vitest";
import { deriveConnectorHealth } from "./connectorHealth";
import type { ConnectorStatus } from "./types";

// Issue #18: the TopBar connector pill must derive from the typed CURRENT
// connector state and FAIL CLOSED — absent, disconnected, Unknown, and degraded
// states must never resolve to the positive/supported health. This is the shared
// mapping the pill binds to; it is contract-tested against the generated
// ConnectorStatus schema (gen/ts), so a shape change is caught here, not in the UI.

const ACCOUNT = "00000000-0000-0000-0000-000000000003";
const CAP_NAMES = [
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

function status(
  connectionState: ConnectorStatus["connectionState"],
  capStatus: ConnectorStatus["capabilities"][number]["status"],
  overrides: Partial<
    Record<(typeof CAP_NAMES)[number], ConnectorStatus["capabilities"][number]["status"]>
  > = {},
): ConnectorStatus {
  return {
    marketplaceAccountId: ACCOUNT,
    connectionState,
    capabilities: CAP_NAMES.map((capability) => ({
      capability,
      status: overrides[capability] ?? capStatus,
    })),
  };
}

describe("deriveConnectorHealth (issue #18, fail closed)", () => {
  it("missing status is unknown (fail closed, never positive)", () => {
    expect(deriveConnectorHealth(undefined)).toBe("unknown");
    expect(deriveConnectorHealth(null)).toBe("unknown");
  });

  it("disconnected connection is disconnected regardless of capability values", () => {
    expect(deriveConnectorHealth(status("disconnected", "supported"))).toBe("disconnected");
    expect(deriveConnectorHealth(status("disconnected", "unknown"))).toBe("disconnected");
  });

  it("connected with any degraded capability is degraded", () => {
    expect(
      deriveConnectorHealth(status("connected", "supported", { price_write: "degraded" })),
    ).toBe("degraded");
  });

  it("connected with any unsupported capability is degraded (non-positive)", () => {
    expect(
      deriveConnectorHealth(status("connected", "supported", { price_write: "unsupported" })),
    ).toBe("degraded");
  });

  it("connected with a still-Unknown capability is probing, never supported", () => {
    expect(
      deriveConnectorHealth(status("connected", "supported", { change_feed: "unknown" })),
    ).toBe("probing");
  });

  it("connected with an empty capability set is probing (fail closed, not supported)", () => {
    expect(
      deriveConnectorHealth({
        marketplaceAccountId: ACCOUNT,
        connectionState: "connected",
        capabilities: [],
      }),
    ).toBe("probing");
  });

  it("supported requires connected AND every capability confirmed supported", () => {
    expect(deriveConnectorHealth(status("connected", "supported"))).toBe("supported");
  });

  it("degraded takes precedence over an also-present Unknown capability (worst wins)", () => {
    expect(
      deriveConnectorHealth(
        status("connected", "supported", { price_write: "degraded", change_feed: "unknown" }),
      ),
    ).toBe("degraded");
  });

  // ── REOPEN residual (issue #18): completeness + uniqueness before health ──
  // An INCOMPLETE or DUPLICATE capability set must never reduce to the positive
  // health. "Unknown never enables" (§15.2): a set that is not exactly the nine
  // §15.2 capabilities, each present once and each attested, fails closed.

  it("incomplete capability set (missing capabilities) is never supported — fails closed", () => {
    // Only three of the nine §15.2 capabilities present, every one supported.
    const incomplete: ConnectorStatus = {
      marketplaceAccountId: ACCOUNT,
      connectionState: "connected",
      capabilities: [
        { capability: "catalog_read", status: "supported" },
        { capability: "owned_offer_read", status: "supported" },
        { capability: "price_write", status: "supported" },
      ],
    };
    const health = deriveConnectorHealth(incomplete);
    expect(health).not.toBe("supported");
    expect(health).toBe("probing");
  });

  it("duplicated capability is never supported — malformed set fails closed", () => {
    // All nine present and supported, PLUS a duplicate entry (ten total).
    const duplicated: ConnectorStatus = {
      marketplaceAccountId: ACCOUNT,
      connectionState: "connected",
      capabilities: [
        ...CAP_NAMES.map((capability) => ({ capability, status: "supported" as const })),
        { capability: "price_write", status: "supported" },
      ],
    };
    const health = deriveConnectorHealth(duplicated);
    expect(health).not.toBe("supported");
    expect(health).toBe("unknown");
  });

  it("any Unknown capability in an otherwise-complete set is never supported", () => {
    // Complete, unique set but one capability still Unknown → not yet probed.
    const withUnknown = status("connected", "supported", { boundary_read: "unknown" });
    expect(deriveConnectorHealth(withUnknown)).not.toBe("supported");
    expect(deriveConnectorHealth(withUnknown)).toBe("probing");
  });

  it("a genuine degradation still surfaces even when the set is also incomplete (worst wins)", () => {
    const incompleteAndDegraded: ConnectorStatus = {
      marketplaceAccountId: ACCOUNT,
      connectionState: "connected",
      capabilities: [
        { capability: "catalog_read", status: "supported" },
        { capability: "price_write", status: "degraded" },
      ],
    };
    expect(deriveConnectorHealth(incompleteAndDegraded)).toBe("degraded");
  });
});
