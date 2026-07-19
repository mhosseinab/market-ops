import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useNow } from "./useNow";

// useNow drives deadline-based freshness (OBS-004): a page left open must
// advance `now` past an offer's transition WITHOUT navigation, and a resumed
// tab must reconcile immediately.
describe("useNow", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-07-18T12:00:00Z"));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("advances `now` when the nearest future transition fires — no navigation needed", () => {
    const t1 = Date.parse("2026-07-18T12:10:00Z");
    const t2 = Date.parse("2026-07-18T13:00:00Z");
    const { result } = renderHook(() => useNow([t1, t2]));

    expect(result.current).toBe(Date.parse("2026-07-18T12:00:00Z"));

    // Advance to just past the first transition; the single timer fires and
    // reconciles `now`, then reschedules to the second.
    act(() => {
      vi.advanceTimersByTime(10 * 60_000 + 1);
    });
    expect(result.current).toBeGreaterThanOrEqual(t1);
    expect(result.current).toBeLessThan(t2);

    act(() => {
      vi.advanceTimersByTime(60 * 60_000);
    });
    expect(result.current).toBeGreaterThanOrEqual(t2);
  });

  it("reconciles immediately on focus/visibility after a suspended tab (timers throttle while hidden)", () => {
    const far = Date.parse("2026-07-18T18:00:00Z");
    const { result } = renderHook(() => useNow([far]));
    expect(result.current).toBe(Date.parse("2026-07-18T12:00:00Z"));

    // Simulate wall-clock jumping forward while the tab was backgrounded and
    // its timer paused, then a focus event.
    vi.setSystemTime(new Date("2026-07-18T17:59:59Z"));
    act(() => {
      window.dispatchEvent(new Event("focus"));
    });
    expect(result.current).toBe(Date.parse("2026-07-18T17:59:59Z"));
  });

  it("schedules nothing when there is no future transition (all in the past)", () => {
    const past = Date.parse("2026-07-18T11:00:00Z");
    const { result } = renderHook(() => useNow([past]));
    act(() => {
      vi.advanceTimersByTime(24 * 60 * 60_000);
    });
    // now only reflects the initial sample; no spurious rescheduling.
    expect(result.current).toBe(Date.parse("2026-07-18T12:00:00Z"));
  });
});
