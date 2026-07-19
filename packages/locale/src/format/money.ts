import type { MessageKey } from "../catalog/keys";
import { LOCALE_PACKS, type LocaleId, type RegionConfig } from "../config";
import { localeGlyphs, toOutputDigits } from "./numbers";

// Money rendering (§9.1 / LOC-008). The renderer ONLY renders via the versioned
// region transform. That transform is UNVERIFIED today (Gate 0a not passed), so
// the exact SOURCE unit is the only display mode — Toman is never inferred. A
// currency/exponent mismatch against the region source unit is a QUARANTINE, not
// an inference. No float touches the mantissa: it is an integer scaled by
// `exponent` and grouped through the locale digit family.

/** Mirrors the Go `Money{mantissa int64, currency, exponent int8}` value. */
export interface Money {
  readonly mantissa: bigint;
  readonly currency: string;
  readonly exponent: number;
}

export interface RenderedMoney {
  /** Localized amount in the source unit, or empty when quarantined. */
  readonly amount: string;
  /** Catalog key for the unit label the caller must render next to `amount`. */
  readonly unitKey: MessageKey;
  /** True when the value cannot be safely displayed and must be quarantined. */
  readonly quarantined: boolean;
  /** Which transform produced this rendering. Only "source" exists in P0. */
  readonly mode: "source" | "display";
}

// Canonical MONEY CORRECTNESS (PRD §4.6 / §9.1): Value = mantissa × 10^exponent,
// matching services/core/internal/money. bigint/string arithmetic only — no
// float, no Number(), no division that loses precision.
function scale(mantissa: bigint, exponent: number): { neg: boolean; int: bigint; frac: string } {
  const neg = mantissa < 0n;
  const abs = neg ? -mantissa : mantissa;
  if (exponent > 0) {
    // Scale UP: multiply by 10^exponent, no fractional part.
    return { neg, int: abs * 10n ** BigInt(exponent), frac: "" };
  }
  if (exponent === 0) {
    return { neg, int: abs, frac: "" };
  }
  // Negative exponent: place the decimal point `-exponent` digits from the right,
  // left-padding the fractional region with zeros when the mantissa is shorter.
  const places = -exponent;
  const s = abs.toString().padStart(places + 1, "0");
  const cut = s.length - places;
  return { neg, int: BigInt(s.slice(0, cut)), frac: s.slice(cut) };
}

/**
 * Render `money` for `region` in `locale`. While the region display transform is
 * unverified, this always renders the exact source unit; if `money.currency`
 * does not match `region.sourceCurrency` the value is quarantined (never
 * inferred into another unit).
 */
export function renderMoney(money: Money, region: RegionConfig, locale: LocaleId): RenderedMoney {
  if (money.currency !== region.sourceCurrency) {
    return { amount: "", unitKey: "money.quarantined", quarantined: true, mode: "source" };
  }

  const { neg, int, frac } = scale(money.mantissa, money.exponent);
  const tag = LOCALE_PACKS[locale].formatLocale;
  const grouped = new Intl.NumberFormat(tag, { useGrouping: true }).format(int);
  const sign = neg ? "−" : ""; // U+2212
  let amount = `${sign}${grouped}`;
  if (frac !== "") {
    const { decimal } = localeGlyphs(locale);
    amount += `${decimal}${toOutputDigits(frac, locale)}`;
  }

  // Source-unit display is the ONLY mode until the region transform is verified.
  return {
    amount,
    unitKey: region.sourceUnitLabelKey as MessageKey,
    quarantined: false,
    mode: "source",
  };
}
