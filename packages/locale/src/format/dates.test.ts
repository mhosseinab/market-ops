import { describe, expect, it } from "vitest";
import { formatDate, toCalendarParts } from "./dates";

// LOC-006: Jalali (fa-IR) and Gregorian (en) are DISPLAY calendars over the same
// absolute UTC storage. These reference conversions are verified against the
// Jalali↔Gregorian calendar, including a leap-year boundary (1403 AP is a 366-day
// leap year — Nowruz fell on 20 March 2024 and Esfand has 30 days).
const REFERENCE: ReadonlyArray<{ utc: string; jalali: [number, number, number]; note: string }> = [
  { utc: "2023-03-21T00:00:00Z", jalali: [1402, 1, 1], note: "Nowruz 1402 (non-leap start)" },
  { utc: "2024-03-20T00:00:00Z", jalali: [1403, 1, 1], note: "Nowruz 1403 (leap year start)" },
  {
    utc: "2025-03-20T00:00:00Z",
    jalali: [1403, 12, 30],
    note: "Esfand 30, 1403 — leap-year last day",
  },
  {
    utc: "2024-03-19T00:00:00Z",
    jalali: [1402, 12, 29],
    note: "Esfand 29, 1402 — non-leap last day",
  },
  { utc: "2025-03-21T00:00:00Z", jalali: [1404, 1, 1], note: "Nowruz 1404" },
];

describe("Jalali display calendar over UTC (LOC-006)", () => {
  for (const { utc, jalali, note } of REFERENCE) {
    it(`${utc} → ${jalali.join("/")} (${note})`, () => {
      const parts = toCalendarParts(utc, "fa-IR", "UTC");
      expect([parts.year, parts.month, parts.day]).toEqual(jalali);
    });
  }

  it("same UTC instant yields the Gregorian date in the en calendar", () => {
    const parts = toCalendarParts("2024-03-20T00:00:00Z", "en", "UTC");
    expect([parts.year, parts.month, parts.day]).toEqual([2024, 3, 20]);
  });

  it("formats fa-IR dates in Persian digits", () => {
    const text = formatDate("2024-03-20T00:00:00Z", "fa-IR", {
      timeZone: "UTC",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
    });
    expect(text).toMatch(/[۰-۹]/);
  });
});
