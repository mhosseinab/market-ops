import type { components } from "@market-ops/gen-ts";

export type Observation = components["schemas"]["Observation"];

// Price-history series (EXT-006): built from the append-only observation store
// (`/observation/observations`). Gaps in the evidence stay gaps — a segment
// break is emitted between two points whose capture times are further apart
// than the target's freshness window; NOTHING interpolates or estimates a
// point inside that gap (docs/06/PRD §4.6 never-cut: no synthetic evidence).

export interface HistoryPoint {
  readonly capturedAt: string;
  /** Raw evidence only — never a Money (money quarantine, §9.1). */
  readonly priceValue: string;
  readonly priceUnit: string;
  readonly quality: Observation["quality"];
}

export interface HistorySegment {
  readonly points: readonly HistoryPoint[];
}

export interface HistorySeries {
  /** One or more CONTIGUOUS segments; a gap lies between consecutive segments. */
  readonly segments: readonly HistorySegment[];
  readonly gapCount: number;
}

// buildHistorySeries sorts the append-only evidence chronologically and splits
// it into gap-free segments. `gapThresholdSeconds` is normally the target's own
// freshnessDeadlineSeconds — a real evidence-driven boundary, never a guessed
// constant duplicated as a magic number at call sites.
export function buildHistorySeries(
  observations: readonly Observation[],
  gapThresholdSeconds: number,
): HistorySeries {
  const sorted = [...observations].sort(
    (a, b) => Date.parse(a.capturedAt) - Date.parse(b.capturedAt),
  );

  const segments: HistoryPoint[][] = [];
  let gapCount = 0;
  let current: HistoryPoint[] = [];

  for (const obs of sorted) {
    const point: HistoryPoint = {
      capturedAt: obs.capturedAt,
      priceValue: obs.price.value,
      priceUnit: obs.price.unit,
      quality: obs.quality,
    };
    const last = current[current.length - 1];
    if (last) {
      const deltaSeconds = (Date.parse(point.capturedAt) - Date.parse(last.capturedAt)) / 1000;
      if (deltaSeconds > gapThresholdSeconds) {
        // A real gap: close the current segment, start a new one. NO point is
        // fabricated in between — the gap is rendered as an actual break.
        segments.push(current);
        current = [];
        gapCount++;
      }
    }
    current.push(point);
  }
  if (current.length > 0) segments.push(current);

  return { segments: segments.map((points) => ({ points })), gapCount };
}
