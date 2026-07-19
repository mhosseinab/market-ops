import { describe, expect, it } from "vitest";
import {
  FRESHNESS_AGING_MAX_MINUTES,
  FRESHNESS_FRESH_MAX_MINUTES,
  freshnessState,
  freshnessStateFromAge,
  freshnessTransitions,
} from "./freshness";

// Deadline-driven derivation (OBS-004): freshness follows the offer's
// AUTHORITATIVE freshnessDeadline, not a fixed 60m/6h age threshold. The
// aging split is WINDOW-RELATIVE (fresh for the first 1/6 of the window).

const CAPTURED = "2026-07-18T12:00:00Z";
const capturedMs = Date.parse(CAPTURED);
const MIN = 60_000;

function at(minutesAfterCapture: number): number {
  return capturedMs + minutesAfterCapture * MIN;
}

describe("freshnessState — standard tier (6h / 360m window) preserves legacy bands", () => {
  const offer = { capturedAt: CAPTURED, freshnessDeadline: "2026-07-18T18:00:00Z" };
  it("fresh for the first 60m (window/6)", () => {
    expect(freshnessState(offer, at(0))).toBe("fresh");
    expect(freshnessState(offer, at(60))).toBe("fresh");
  });
  it("aging after 60m and before the deadline", () => {
    expect(freshnessState(offer, at(61))).toBe("aging");
    expect(freshnessState(offer, at(359))).toBe("aging");
  });
  it("stale exactly AT the deadline (exclusive fresh window)", () => {
    expect(freshnessState(offer, at(360))).toBe("stale");
    expect(freshnessState(offer, at(361))).toBe("stale");
  });
});

describe("freshnessState — priority tier (60m window) does NOT get the 6h allowance", () => {
  const offer = { capturedAt: CAPTURED, freshnessDeadline: "2026-07-18T13:00:00Z" };
  it("fresh only for the first 10m (window/6)", () => {
    expect(freshnessState(offer, at(0))).toBe("fresh");
    expect(freshnessState(offer, at(10))).toBe("fresh");
    expect(freshnessState(offer, at(11))).toBe("aging");
  });
  it("stale exactly at the 60m deadline — NOT still fresh under a fixed 60m threshold", () => {
    expect(freshnessState(offer, at(59))).toBe("aging");
    expect(freshnessState(offer, at(60))).toBe("stale");
  });
});

describe("freshnessState — background tier (24h window) stays usable to its deadline", () => {
  const offer = { capturedAt: CAPTURED, freshnessDeadline: "2026-07-19T12:00:00Z" };
  it("fresh for the first 4h (window/6 = 240m)", () => {
    expect(freshnessState(offer, at(240))).toBe("fresh");
    expect(freshnessState(offer, at(241))).toBe("aging");
  });
  it("aging — usable — right up to the 24h deadline", () => {
    expect(freshnessState(offer, at(1439))).toBe("aging");
    expect(freshnessState(offer, at(1440))).toBe("stale");
  });
});

describe("freshnessState — fails closed to stale on bad input", () => {
  it("missing/unparseable deadline", () => {
    expect(freshnessState({ capturedAt: CAPTURED, freshnessDeadline: "" }, at(0))).toBe("stale");
    expect(freshnessState({ capturedAt: CAPTURED, freshnessDeadline: "nope" }, at(0))).toBe(
      "stale",
    );
  });
  it("missing/unparseable capturedAt", () => {
    expect(
      freshnessState({ capturedAt: "", freshnessDeadline: "2026-07-18T18:00:00Z" }, at(0)),
    ).toBe("stale");
  });
  it("non-positive window (deadline at/before capture)", () => {
    expect(
      freshnessState({ capturedAt: CAPTURED, freshnessDeadline: CAPTURED }, capturedMs - 1),
    ).toBe("stale");
    expect(
      freshnessState({ capturedAt: CAPTURED, freshnessDeadline: "2026-07-18T11:00:00Z" }, at(-30)),
    ).toBe("stale");
  });
});

describe("freshnessTransitions — absolute ms timestamps of state changes", () => {
  it("returns fresh→aging and aging→stale boundaries for a valid offer", () => {
    const offer = { capturedAt: CAPTURED, freshnessDeadline: "2026-07-18T18:00:00Z" };
    expect(freshnessTransitions(offer)).toEqual([at(60), at(360)]);
  });
  it("returns [] when the deadline is unparseable (already stale, no future transition)", () => {
    expect(freshnessTransitions({ capturedAt: CAPTURED, freshnessDeadline: "" })).toEqual([]);
  });
  it("returns [] for a non-positive window", () => {
    expect(freshnessTransitions({ capturedAt: CAPTURED, freshnessDeadline: CAPTURED })).toEqual([]);
  });
});

describe("freshnessStateFromAge — deadline-LESS path (market events only)", () => {
  it("bands on fixed minute thresholds", () => {
    expect(freshnessStateFromAge(FRESHNESS_FRESH_MAX_MINUTES)).toBe("fresh");
    expect(freshnessStateFromAge(FRESHNESS_FRESH_MAX_MINUTES + 1)).toBe("aging");
    expect(freshnessStateFromAge(FRESHNESS_AGING_MAX_MINUTES)).toBe("aging");
    expect(freshnessStateFromAge(FRESHNESS_AGING_MAX_MINUTES + 1)).toBe("stale");
  });
});
