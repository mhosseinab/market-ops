import {
  formatDate,
  formatInteger,
  type LocaleId,
  type Money,
  REGION_IR,
  type RenderedMoney,
  renderMoney,
} from "@market-ops/locale";
import type { MoneyAmount } from "./types";

// Formatting helpers bound to the active locale. Money always renders through the
// versioned region transform (source-unit only until Gate 0a); no float touches
// the mantissa. The wire carries the int64 mantissa as an exact signed-decimal
// STRING (contracts MoneyAmount, PRD §9.1), so `toMoney` parses it DIRECTLY to
// bigint — never through a JavaScript-number intermediate that would round at
// 2^53. Dates are a display calendar over UTC storage (Jalali for fa-IR). Digit
// family is a locale property applied by the Intl formatters, never branched here.

// Signed base-10 decimal string, mirroring the contract pattern `^-?[0-9]+$`.
const MANTISSA_PATTERN = /^-?[0-9]+$/;
// Signed int64 range — the authoritative width of a Money mantissa (§9.1).
const INT64_MIN = -(2n ** 63n);
const INT64_MAX = 2n ** 63n - 1n;

/**
 * Widen the wire `MoneyAmount` (int64 mantissa as a signed-decimal STRING) to
 * the exact bigint `Money`. Fails closed (quarantine over inference, never a
 * coerced default) on a non-decimal string or a value outside signed int64
 * range — no float, no JavaScript-number intermediate.
 */
export function toMoney(amount: MoneyAmount): Money {
  const raw = amount.mantissa;
  if (typeof raw !== "string" || !MANTISSA_PATTERN.test(raw)) {
    throw new Error(`money: mantissa ${JSON.stringify(raw)} is not a signed int64 decimal string`);
  }
  const mantissa = BigInt(raw);
  if (mantissa < INT64_MIN || mantissa > INT64_MAX) {
    throw new Error(`money: mantissa ${raw} is outside signed int64 range`);
  }
  return {
    mantissa,
    currency: amount.currency,
    exponent: amount.exponent,
  };
}

/** Encode an exact bigint mantissa back to the wire `MoneyAmount.mantissa` string. */
export function mantissaToWire(mantissa: bigint): string {
  if (mantissa < INT64_MIN || mantissa > INT64_MAX) {
    throw new Error(`money: mantissa ${mantissa} is outside signed int64 range`);
  }
  return mantissa.toString();
}

export function renderAmount(amount: MoneyAmount, locale: LocaleId): RenderedMoney {
  return renderMoney(toMoney(amount), REGION_IR, locale);
}

/** Whole minutes elapsed between an ISO capture instant and now (never negative). */
export function ageMinutes(iso: string, now: number = Date.now()): number {
  const captured = new Date(iso).getTime();
  if (Number.isNaN(captured)) return 0;
  return Math.max(0, Math.floor((now - captured) / 60_000));
}

export function formatInstant(iso: string, locale: LocaleId): string {
  return formatDate(iso, locale, {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function formatCount(value: number, locale: LocaleId): string {
  return formatInteger(value, locale);
}
