import type { EventType, MarketEvent, QualityState } from "./types";

// Event-detail evidence, split into the four authoritative provenance categories
// the design requires (design/README.md — the four-way separation). Each category
// is derived ONLY from MarketEvent fields that authoritatively belong to it, so a
// field can never be rendered under the wrong provenance (issue #97):
//   - observed  ← the observed market condition (event type) + its cited observation
//   - dk        ← the DK-sourced evidence reference and its quality state
//   - config    ← the governing materiality-threshold version (seller configuration)
//   - inference ← ALWAYS absent: the MarketEvent contract carries no model output.
// Optional categories are `null` when their field is absent — never fabricated
// with a convenient field or generic explanation.

export type ObservedEvidence = {
  /** The observed market condition (EventType) — always present. */
  type: EventType;
  /** The cited observation backing the fact, when the event has one. */
  observationId: string | null;
};

export type DkEvidence = {
  /** Opaque reference to the cited DK-sourced evidence, when present. */
  evidenceRef: string | null;
  /** Quality state of the cited marketplace evidence. */
  quality: QualityState;
};

export type ConfigEvidence = {
  /** The materiality-threshold version that fired the event (EVT-002). */
  thresholdVersion: number | null;
};

export type EventEvidence = {
  observed: ObservedEvidence;
  dk: DkEvidence;
  config: ConfigEvidence;
  /** No inference field exists in the MarketEvent contract; always null. */
  inference: null;
};

export function deriveEventEvidence(event: MarketEvent): EventEvidence {
  return {
    observed: {
      type: event.type,
      observationId: event.evidenceObservationId ?? null,
    },
    dk: {
      evidenceRef: event.evidenceRef ?? null,
      quality: event.evidenceQuality as QualityState,
    },
    config: {
      thresholdVersion: typeof event.thresholdVersion === "number" ? event.thresholdVersion : null,
    },
    inference: null,
  };
}
