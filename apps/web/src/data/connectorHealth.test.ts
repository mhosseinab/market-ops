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
});
