import { LOCALE_PACKS, type LocaleId, REGION_IR } from "../config";

// Calendar display over ABSOLUTE UTC storage (LOC-006). Storage is always a UTC
// instant; the DISPLAY calendar (Jalali for fa-IR via …-ca-persian, Gregorian
// for en) is a locale property applied by `Intl.DateTimeFormat`. No calendar
// branch lives in a view — the locale tag carries the calendar.

/** A UTC instant: a `Date`, epoch milliseconds, or an ISO-8601 string. */
export type Instant = Date | number | string;

function toDate(instant: Instant): Date {
  const d = instant instanceof Date ? instant : new Date(instant);
  if (Number.isNaN(d.getTime())) {
    throw new RangeError("dates: invalid instant");
  }
  return d;
}

const dtCache = new Map<string, Intl.DateTimeFormat>();

function formatter(locale: LocaleId, timeZone: string, opts: Intl.DateTimeFormatOptions) {
  const tag = LOCALE_PACKS[locale].formatLocale;
  const key = `${tag}|${timeZone}|${JSON.stringify(opts)}`;
  let dt = dtCache.get(key);
  if (!dt) {
    dt = new Intl.DateTimeFormat(tag, { timeZone, ...opts });
    dtCache.set(key, dt);
  }
  return dt;
}

/**
 * Format a UTC instant for display in the active locale's calendar. Defaults to
 * the IR region timezone (Asia/Tehran); pass `timeZone:"UTC"` for calendar-
 * boundary reference tests.
 */
export function formatDate(
  instant: Instant,
  locale: LocaleId,
  opts: Intl.DateTimeFormatOptions & { timeZone?: string } = {},
): string {
  const { timeZone = REGION_IR.timezone, ...rest } = opts;
  const style: Intl.DateTimeFormatOptions =
    Object.keys(rest).length > 0 ? rest : { year: "numeric", month: "long", day: "numeric" };
  return formatter(locale, timeZone, style).format(toDate(instant));
}

/** Numeric calendar parts in LATIN digits — locale-independent for assertions. */
export interface CalendarParts {
  readonly year: number;
  readonly month: number;
  readonly day: number;
}

/**
 * Resolve the display-calendar year/month/day of a UTC instant, in the given
 * locale's calendar, as plain integers. Used by the reference-conversion tests
 * (LOC-006) and by any storage-agnostic consumer that needs the parts.
 */
export function toCalendarParts(
  instant: Instant,
  locale: LocaleId,
  timeZone = "UTC",
): CalendarParts {
  // Force Latin digits so parts are locale-independent integers, without
  // duplicating the `-nu-` subtag already present in `formatLocale`.
  const parts = new Intl.DateTimeFormat(LOCALE_PACKS[locale].formatLocale, {
    timeZone,
    numberingSystem: "latn",
    year: "numeric",
    month: "numeric",
    day: "numeric",
  }).formatToParts(toDate(instant));
  const get = (type: Intl.DateTimeFormatPartTypes) =>
    Number.parseInt(parts.find((p) => p.type === type)?.value ?? "", 10);
  return { year: get("year"), month: get("month"), day: get("day") };
}
