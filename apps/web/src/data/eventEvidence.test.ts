import { describe, expect, it } from "vitest";
import { deriveEventEvidence } from "./eventEvidence";
import type { MarketEvent } from "./types";

// A minimal MarketEvent with every evidence-bearing field populated. Each field
// belongs to exactly ONE authoritative provenance category; the derivation must
// place it there and nowhere else (issue #97).
const fullEvent: MarketEvent = {
  id: "11111111-1111-1111-1111-111111111111",
  marketplaceAccountId: "22222222-2222-2222-2222-222222222222",
  variantId: "33333333-3333-3333-3333-333333333333",
  type: "competitor_price",
  severity: "warning",
  state: "open",
  factors: { exposure: { known: false }, confidenceBp: 9200, urgencyBp: 6000 },
  thresholdVersion: 3,
  evidenceObservationId: "44444444-4444-4444-4444-444444444444",
  evidenceQuality: "verified",
  evidenceRef: "obs:route_c:8842213",
  firstDetectedAt: "2026-07-17T06:00:00Z",
  lastEvidenceAt: "2026-07-17T09:30:00Z",
  expiresAt: "2026-07-18T06:00:00Z",
  evidenceUpdateCount: 2,
};

describe("deriveEventEvidence (issue #97 — authoritative provenance separation)", () => {
  it("places the governing threshold version only under seller configuration", () => {
    const ev = deriveEventEvidence(fullEvent);
    expect(ev.config.thresholdVersion).toBe(3);
    // A materiality threshold is seller/governing configuration — never a DK signal
    // and never an observed fact.
    expect(JSON.stringify(ev.observed)).not.toContain("3");
    expect(ev.dk).not.toHaveProperty("thresholdVersion");
    expect(ev.observed).not.toHaveProperty("thresholdVersion");
  });

  it("renders DK/marketplace evidence only from the DK-sourced reference and its quality", () => {
    const ev = deriveEventEvidence(fullEvent);
    expect(ev.dk.evidenceRef).toBe("obs:route_c:8842213");
    expect(ev.dk.quality).toBe("verified");
    // The DK-sourced reference must never leak into the observed panel.
    expect(ev.observed).not.toHaveProperty("evidenceRef");
  });

  it("renders the observed fact from the observed event condition and its cited observation", () => {
    const ev = deriveEventEvidence(fullEvent);
    expect(ev.observed.type).toBe("competitor_price");
    expect(ev.observed.observationId).toBe("44444444-4444-4444-4444-444444444444");
  });

  it("carries no model inference — the MarketEvent contract has no inference field", () => {
    const ev = deriveEventEvidence(fullEvent);
    expect(ev.inference).toBeNull();
  });

  it("marks each optional category unavailable (null) when its field is absent, never fabricated", () => {
    const bare: MarketEvent = {
      ...fullEvent,
      thresholdVersion: undefined,
      evidenceObservationId: undefined,
      evidenceRef: undefined,
    };
    const ev = deriveEventEvidence(bare);
    expect(ev.config.thresholdVersion).toBeNull();
    expect(ev.observed.observationId).toBeNull();
    expect(ev.dk.evidenceRef).toBeNull();
    expect(ev.inference).toBeNull();
    // The observed condition is always present (the event type is authoritative).
    expect(ev.observed.type).toBe("competitor_price");
  });
});
