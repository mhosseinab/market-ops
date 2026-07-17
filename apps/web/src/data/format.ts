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
// the mantissa — the int64 mantissa is widened to bigint exactly. Dates are a
// display calendar over UTC storage (Jalali for fa-IR). Digit family is a locale
// property applied by the Intl formatters, never branched here.

/** Widen the wire `MoneyAmount` (int64 as number) to the exact bigint `Money`. */
export function toMoney(amount: MoneyAmount): Money {
  return {
    mantissa: BigInt(amount.mantissa),
    currency: amount.currency,
    exponent: amount.exponent,
  };
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
