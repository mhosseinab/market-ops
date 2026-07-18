// Locale + region configuration as DATA (PRD §11.1/§11.2, LOC-001/010).
// Nothing about language, direction, digits, currency, calendar, or marketplace
// is branched on in code: switching the active `LocaleId` re-derives everything
// from these records. A second locale/region is a new entry here plus a catalog
// and a connector binding — no core diff (LOC-010).

/** BCP-47 language tag used to build every `Intl` formatter. */
export type LocaleId = "fa-IR" | "en";

/** Layout direction, driven onto the document root; never inferred in a view. */
export type Direction = "rtl" | "ltr";

/**
 * A locale pack descriptor (PRD §11.1). The message catalog itself lives in
 * `catalog/*` keyed by `id`; this record carries the non-message facets.
 */
export interface LocalePack {
  readonly id: LocaleId;
  /** BCP-47 tag with calendar/numbering extensions for `Intl` formatters. */
  readonly formatLocale: string;
  readonly dir: Direction;
  readonly lang: string;
  /**
   * Digit families this locale ACCEPTS on input (normalized to Latin at the
   * boundary, LOC-007). The first entry is also the OUTPUT family.
   */
  readonly outputNumberingSystem: "latn" | "arabext";
  /** Display calendar (LOC-006). Jalali for fa-IR, Gregorian for en. */
  readonly calendar: "persian" | "gregory";
}

/**
 * Region configuration (PRD §11.2). `displayTransform.verified` gates Toman
 * display: while UNVERIFIED (Gate 0a not passed) the money renderer shows the
 * exact source unit only and never infers a display unit (§9.1).
 */
export interface RegionConfig {
  readonly id: string;
  /** ISO-4217 code of the raw marketplace source unit. */
  readonly sourceCurrency: string;
  /** Catalog key for the source-unit label (never a hardcoded literal). */
  readonly sourceUnitLabelKey: string;
  /** Catalog key for the marketplace brand name (parameterized, LOCALIZATION.md). */
  readonly marketplaceNameKey: string;
  readonly timezone: string;
  /**
   * Display-unit transform. `verified:false` ⇒ disabled: source-unit display is
   * the only mode. When a future region verifies a transform it supplies the
   * divisor as an integer (no float on any money path).
   */
  readonly displayTransform: {
    readonly verified: boolean;
    readonly displayUnitLabelKey?: string;
    readonly divisorPow10?: number;
  };
}

export const LOCALE_PACKS: Readonly<Record<LocaleId, LocalePack>> = {
  "fa-IR": {
    id: "fa-IR",
    formatLocale: "fa-IR-u-ca-persian-nu-arabext",
    dir: "rtl",
    lang: "fa",
    outputNumberingSystem: "arabext",
    calendar: "persian",
  },
  en: {
    id: "en",
    formatLocale: "en-US-u-ca-gregory-nu-latn",
    dir: "ltr",
    lang: "en",
    outputNumberingSystem: "latn",
    calendar: "gregory",
  },
};

/**
 * IR region. Rial is the source unit; the Rial→Toman display transform is
 * UNVERIFIED today (Gate 0a), so it stays disabled — source-unit display only.
 */
export const REGION_IR: RegionConfig = {
  id: "IR",
  sourceCurrency: "IRR",
  sourceUnitLabelKey: "unit.rial",
  marketplaceNameKey: "marketplace.name",
  timezone: "Asia/Tehran",
  displayTransform: {
    verified: false,
    displayUnitLabelKey: "unit.toman",
    divisorPow10: 1,
  },
};

export const DEFAULT_LOCALE: LocaleId = "fa-IR";
export const FALLBACK_LOCALE: LocaleId = "en";
