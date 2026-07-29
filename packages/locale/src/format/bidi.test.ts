import { describe, expect, it } from "vitest";
import { LRI, ltrIsolate, PDI } from "./bidi";

// LOC-005 / design/LOCALIZATION.md: a technical identifier interpolated into a
// PLAIN STRING (aria-label, title, an assistive-technology announcement) carries no
// CSS, so `LtrToken`'s isolation does not apply and the Unicode bidirectional
// algorithm can transpose its parts. `ltrIsolate` is the plain-string equivalent.
// It was shipped and exported with no direct coverage at all — the documented
// empty-string branch was uncovered everywhere (issue #87, W6).

describe("ltrIsolate — plain-string LTR isolation of technical identifiers (LOC-005)", () => {
  it("W6: wraps a non-empty identifier in an explicit LTR isolate", () => {
    const wrapped = ltrIsolate("8842213:seller-1");
    expect(wrapped).toBe(`${LRI}8842213:seller-1${PDI}`);
    expect(wrapped.startsWith(LRI)).toBe(true);
    expect(wrapped.endsWith(PDI)).toBe(true);
  });

  it("W6: returns an EMPTY value unchanged (never two invisible controls)", () => {
    // Isolating nothing would produce a two-character string an assistive
    // technology announces as non-empty. The documented branch, now covered.
    expect(ltrIsolate("")).toBe("");
    expect(ltrIsolate("")).not.toContain(LRI);
    expect(ltrIsolate("")).not.toContain(PDI);
  });

  it("W6: round-trips — stripping the isolate restores the original identifier", () => {
    for (const value of ["SKU-1", "https://example.test/a?b=c", "۱۲۳", "کالا-۱"]) {
      const wrapped = ltrIsolate(value);
      expect(wrapped.slice(LRI.length, wrapped.length - PDI.length)).toBe(value);
    }
  });

  it("W6: is NOT idempotent — a second call NESTS, so callers wrap exactly once", () => {
    // Documented, deliberate behavior: the function does not inspect its input for
    // existing controls. Pinning it stops a future "optimization" from silently
    // stripping a caller's intentional nesting.
    const once = ltrIsolate("SKU-1");
    expect(ltrIsolate(once)).toBe(`${LRI}${once}${PDI}`);
  });

  it("W6: carries NO locale branch — the same input yields the same output always", () => {
    // LOC-001: direction is a property of the IDENTIFIER, not of the reader's
    // language, so this must not vary with locale, calendar, or region.
    expect(ltrIsolate("SKU-1")).toBe(ltrIsolate("SKU-1"));
    expect(LRI).toBe("⁦");
    expect(PDI).toBe("⁩");
  });
});
