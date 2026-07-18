import { beforeEach, describe, expect, it } from "vitest";
import { counterValue, gauge, gaugeValue, incr, resetCounters } from "./observability";

describe("observability gauges (docs/14: queue depth is a tracked REAL metric, never a placeholder)", () => {
  beforeEach(() => {
    resetCounters();
  });

  it("gauge reports the LATEST set value, never an accumulated sum", () => {
    gauge("queue_depth", 5);
    expect(gaugeValue("queue_depth")).toBe(5);
    gauge("queue_depth", 2);
    // A real gauge reflects the current state (2), NOT 5+2 — distinguishing it
    // from a monotonic counter (S30 carry-forward: queue_depth was a no-op incr(0)).
    expect(gaugeValue("queue_depth")).toBe(2);
  });

  it("counters still accumulate independently of gauges (distinct metric spaces)", () => {
    incr("upload_accepted", {}, 3);
    incr("upload_accepted", {}, 1);
    expect(counterValue("upload_accepted")).toBe(4);
    expect(gaugeValue("upload_accepted")).toBe(0);
  });

  it("an unset gauge reads 0, never undefined/NaN", () => {
    expect(gaugeValue("queue_depth")).toBe(0);
  });
});
