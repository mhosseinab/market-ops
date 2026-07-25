import { describe, expect, it } from "vitest";
import { type Capability, captureEnabled, degradationReason } from "./capability";

describe("capability (Unknown never enables — PRD §4.6)", () => {
  it("ONLY 'ready' enables capture; every other state fails closed", () => {
    expect(captureEnabled("ready")).toBe(true);
    for (const cap of [
      "unknown",
      "revoked",
      "disabled",
      // #149: a revocation awaiting server confirmation disables capture
      // IMMEDIATELY — it is never a half-enabled state.
      "revocation_pending",
    ] as Capability[]) {
      expect(captureEnabled(cap)).toBe(false);
    }
  });

  it("exposes a stable, locale-neutral degradation token for the popup", () => {
    expect(degradationReason("ready")).toBeNull();
    expect(degradationReason("unknown")).toBe("not_paired");
    expect(degradationReason("revoked")).toBe("credential_revoked");
    expect(degradationReason("disabled")).toBe("capture_disabled");
    // #149: pending is VISIBLY DISTINCT from confirmed revocation — the popup
    // must never claim a kill switch completed when the server has not confirmed.
    expect(degradationReason("revocation_pending")).toBe("revocation_pending");
    expect(degradationReason("revocation_pending")).not.toBe(degradationReason("revoked"));
  });
});
