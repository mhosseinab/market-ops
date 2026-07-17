// Public surface of the locale pack (PRD §11, design/LOCALIZATION.md). The web
// app and extension consume ONLY these exports — data-driven locale/region
// config, ICU catalogs, telemetry-aware i18next factory, and `Intl`-based
// formatters. No locale/calendar/currency/direction branch lives outside here.

export { en } from "./catalog/en";
export { faIR } from "./catalog/fa-IR";
export { type Catalog, MESSAGE_KEYS, type MessageKey } from "./catalog/keys";
export {
  DEFAULT_LOCALE,
  type Direction,
  FALLBACK_LOCALE,
  LOCALE_PACKS,
  type LocaleId,
  type LocalePack,
  REGION_IR,
  type RegionConfig,
} from "./config";
export { type CalendarParts, formatDate, type Instant, toCalendarParts } from "./format/dates";

export { normalizeDigits, parseNumericInput } from "./format/digits";
export { type Money, type RenderedMoney, renderMoney } from "./format/money";
export {
  formatBasisPoints,
  formatInteger,
  type LocaleGlyphs,
  localeGlyphs,
  toOutputDigits,
} from "./format/numbers";
export {
  type CreateI18nOptions,
  createI18n,
  directionOf,
  type MissingKeyEvent,
  type MissingKeyTelemetry,
  translate,
} from "./i18n";

export {
  buildPseudoCatalog,
  isPseudoTranslated,
  PSEUDO_CLOSE,
  PSEUDO_DIR,
  PSEUDO_ID,
  PSEUDO_OPEN,
  pseudoMessage,
} from "./pseudo";
