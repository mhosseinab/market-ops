import { describe, expect, it } from "vitest";
import { buildHistorySeries, type Observation } from "./history";

function obs(capturedAt: string, value: string): Observation {
  return {
    id: `id-${capturedAt}`,
    targetId: "t1",
    marketplaceAccountId: "acct-1",
    offerIdentity: "111:seller-a",
    route: "route_b",
    parserVersion: "dk-product@1.0.0",
    sourceType: "public-web-endpoint",
    evidenceRef: `ev-${capturedAt}`,
    price: { text: `${value} IRR-rial`, value, unit: "IRR-rial" },
    availabilityStatus: "in_stock",
    quality: "verified",
    capturedAt,
  } as Observation;
}

describe("history — EXT-006 gap-preserving price history (never-cut: no synthetic point)", () => {
  it("a single contiguous run of evidence is ONE segment with no gap", () => {
    const series = buildHistorySeries(
      [obs("2026-07-18T09:00:00Z", "100000"), obs("2026-07-18T09:30:00Z", "100000")],
      3600,
    );
    expect(series.gapCount).toBe(0);
    expect(series.segments).toHaveLength(1);
    expect(series.segments[0]?.points).toHaveLength(2);
  });

  it("a delta beyond the freshness window opens a REAL gap — no fabricated point bridges it", () => {
    const series = buildHistorySeries(
      [
        obs("2026-07-18T09:00:00Z", "100000"),
        // 5 hours later, threshold is 1 hour (3600s) — a real gap.
        obs("2026-07-18T14:00:00Z", "105000"),
      ],
      3600,
    );
    expect(series.gapCount).toBe(1);
    expect(series.segments).toHaveLength(2);
    expect(series.segments[0]?.points).toHaveLength(1);
    expect(series.segments[1]?.points).toHaveLength(1);
    // The only two points present are the two REAL captures — nothing invented.
    const allPoints = series.segments.flatMap((s) => s.points);
    expect(allPoints.map((p) => p.priceValue)).toEqual(["100000", "105000"]);
  });

  it("sorts out-of-order evidence chronologically before segmenting", () => {
    const series = buildHistorySeries(
      [obs("2026-07-18T09:30:00Z", "b"), obs("2026-07-18T09:00:00Z", "a")],
      3600,
    );
    expect(series.segments[0]?.points.map((p) => p.priceValue)).toEqual(["a", "b"]);
  });

  it("an empty evidence set yields zero segments and zero gaps — never a placeholder point", () => {
    const series = buildHistorySeries([], 3600);
    expect(series.segments).toHaveLength(0);
    expect(series.gapCount).toBe(0);
  });
});
