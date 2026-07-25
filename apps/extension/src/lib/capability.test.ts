import { describe, expect, it } from "vitest";
import {
  type Capability,
  captureEnabled,
  degradationReason,
  REVOCATION_UNCONFIRMED_TOKEN,
} from "./capability";

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
      // #149 fix 3: the "could not confirm" quarantine terminal also fails
      // closed — quarantine over inference (§4.6), never a half-enabled state.
      "revocation_unconfirmed",
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
    // #149 fix 3: the quarantine terminal has its OWN token. It must never
    // collapse into `credential_revoked` (which would falsely claim a completed
    // kill switch) nor into `revocation_pending` (which promises an active
    // retry under the pending marker) — the whole point is that it is a
    // distinct, observable outcome.
    // Non-vacuity: assert the token really is a non-empty string first — an
    // absent switch arm would otherwise return undefined and compare equal to an
    // absent export.
    expect(typeof REVOCATION_UNCONFIRMED_TOKEN).toBe("string");
    expect(REVOCATION_UNCONFIRMED_TOKEN.length).toBeGreaterThan(0);
    expect(degradationReason("revocation_unconfirmed")).toBe(REVOCATION_UNCONFIRMED_TOKEN);
    expect(degradationReason("revocation_unconfirmed")).not.toBe(degradationReason("revoked"));
    expect(degradationReason("revocation_unconfirmed")).not.toBe(
      degradationReason("revocation_pending"),
    );
  });
});
