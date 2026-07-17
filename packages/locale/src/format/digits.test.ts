import { describe, expect, it } from "vitest";
import { normalizeDigits, parseNumericInput } from "./digits";
import { formatInteger, toOutputDigits } from "./numbers";

// LOC-007: declared digit families normalize BEFORE calculation; the output
// family is a locale property applied by the formatter.
describe("digit normalization at the input boundary (LOC-007)", () => {
  it("normalizes Persian digits to Latin", () => {
    expect(normalizeDigits("۱۲۳۴۵۶۷۸۹۰")).toBe("1234567890");
  });

  it("normalizes Arabic-Indic digits to Latin", () => {
    expect(normalizeDigits("٠١٢٣٤٥٦٧٨٩")).toBe("0123456789");
  });

  it("is idempotent on Latin input", () => {
    expect(normalizeDigits("42")).toBe("42");
  });

  it("parses mixed Persian/Latin grouped input to a canonical number", () => {
    expect(parseNumericInput("۱٬۲۳۴٬۵۶۷")).toBe("1234567");
    expect(parseNumericInput("۱۲٫۵")).toBe("12.5");
  });

  it("rejects non-numeric input rather than inferring", () => {
    expect(parseNumericInput("abc")).toBeNull();
    expect(parseNumericInput("")).toBeNull();
  });

  it("renders output digits per locale family", () => {
    expect(formatInteger(1234567n, "fa-IR")).toBe("۱٬۲۳۴٬۵۶۷");
    expect(formatInteger(1234567, "en")).toBe("1,234,567");
    expect(toOutputDigits("2024", "fa-IR")).toBe("۲۰۲۴");
  });
});
