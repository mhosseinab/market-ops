import { describe, expect, it } from "vitest";
import { toMoney } from "./format";
import type { MoneyAmount } from "./types";

// Regression suite for issue #73 (never-cut MONEY CORRECTNESS, PRD §9.1): the
// int64 Money mantissa crosses the contract boundary as an exact signed-decimal
// STRING, and `toMoney` converts that string DIRECTLY to bigint — never through
// a JavaScript-number intermediate that would silently round at 2^53.

// Authoritative boundary mantissas (as they appear on the wire: strings).
const VECTORS: ReadonlyArray<{ name: string; mantissa: string; exponent: number }> = [
  { name: "2^53-1", mantissa: "9007199254740991", exponent: 0 },
  { name: "2^53", mantissa: "9007199254740992", exponent: 0 },
  // The issue's deterministic reproduction: 2^53+1 is NOT representable as an
  // exact JavaScript number (it rounds down to 2^53), so a number intermediate
  // would corrupt it.
  { name: "2^53+1 (reproduction)", mantissa: "9007199254740993", exponent: 0 },
  { name: "MaxInt64", mantissa: "9223372036854775807", exponent: 0 },
  { name: "MinInt64", mantissa: "-9223372036854775808", exponent: 0 },
  { name: "negative large", mantissa: "-9007199254740993", exponent: -2 },
  { name: "small positive with exponent", mantissa: "12345", exponent: 2 },
  { name: "zero", mantissa: "0", exponent: 0 },
];

describe("toMoney string→bigint widening (#73)", () => {
  it("preserves every boundary mantissa exactly when it arrives as a wire string", () => {
    for (const v of VECTORS) {
      // Simulate the real transport: a JSON payload from the gateway. Because
      // the mantissa is string-encoded, JSON.parse cannot round it.
      const wire = `{"mantissa":"${v.mantissa}","currency":"IRR","exponent":${v.exponent}}`;
      const amount = JSON.parse(wire) as MoneyAmount;
      const money = toMoney(amount);
      expect(money.mantissa).toBe(BigInt(v.mantissa));
      expect(money.mantissa.toString()).toBe(v.mantissa);
      expect(money.exponent).toBe(v.exponent);
      expect(money.currency).toBe("IRR");
    }
  });

  it("documents WHY a numeric wire encoding is unsafe (2^53+1 rounds under JSON.parse)", () => {
    // The pre-fix bug: an int64 delivered as a JSON number is already corrupted
    // before any BigInt widening can help.
    const numericWire = `{"mantissa":9007199254740993,"currency":"IRR","exponent":0}`;
    const corrupted = JSON.parse(numericWire).mantissa as number;
    expect(corrupted).toBe(9007199254740992); // lost the low bit
    expect(BigInt(corrupted)).not.toBe(9007199254740993n);
    // The string encoding is the only representation that survives the boundary.
    const stringWire = `{"mantissa":"9007199254740993","currency":"IRR","exponent":0}`;
    expect(toMoney(JSON.parse(stringWire) as MoneyAmount).mantissa).toBe(9007199254740993n);
  });

  it("fails closed on a mantissa above int64 range", () => {
    const overflow = {
      mantissa: "9223372036854775808",
      currency: "IRR",
      exponent: 0,
    } as MoneyAmount;
    expect(() => toMoney(overflow)).toThrow();
  });

  it("fails closed on a mantissa below int64 range", () => {
    const underflow = {
      mantissa: "-9223372036854775809",
      currency: "IRR",
      exponent: 0,
    } as MoneyAmount;
    expect(() => toMoney(underflow)).toThrow();
  });

  it("fails closed on a non-decimal mantissa string (never inference)", () => {
    for (const bad of ["12.5", "1e3", "abc", "", "-", "+1", " 10", "0x10", "10 "]) {
      const amount = { mantissa: bad, currency: "IRR", exponent: 0 } as MoneyAmount;
      expect(() => toMoney(amount), `mantissa=${JSON.stringify(bad)}`).toThrow();
    }
  });
});
