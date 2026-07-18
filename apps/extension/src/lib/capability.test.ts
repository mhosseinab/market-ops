import { describe, expect, it } from "vitest";
import { type Capability, captureEnabled, degradationReason } from "./capability";

describe("capability (Unknown never enables — PRD §4.6)", () => {
  it("ONLY 'ready' enables capture; every other state fails closed", () => {
    expect(captureEnabled("ready")).toBe(true);
    for (const cap of ["unknown", "revoked", "disabled"] as Capability[]) {
      expect(captureEnabled(cap)).toBe(false);
    }
  });

  it("exposes a stable, locale-neutral degradation token for the popup", () => {
    expect(degradationReason("ready")).toBeNull();
    expect(degradationReason("unknown")).toBe("not_paired");
    expect(degradationReason("revoked")).toBe("credential_revoked");
    expect(degradationReason("disabled")).toBe("capture_disabled");
  });
});
