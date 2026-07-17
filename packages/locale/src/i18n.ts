import i18next, { type i18n as I18n } from "i18next";
import ICU from "i18next-icu";
import { en } from "./catalog/en";
import { faIR } from "./catalog/fa-IR";
import type { Catalog, MessageKey } from "./catalog/keys";
import { DEFAULT_LOCALE, FALLBACK_LOCALE, LOCALE_PACKS, type LocaleId } from "./config";

// i18next + ICU (plan §4.5). Persian-first with an English AUTHORING fallback.
// Missing-key telemetry (LOC-004) is emitted through a caller-supplied sink; a
// raw key never surfaces because every fa-IR gap resolves to the complete en
// catalog. Framework types never leave this package.

export interface MissingKeyEvent {
  readonly key: string;
  /** Locale that was requested but lacked the key. */
  readonly requested: string;
  /** Locale that actually served the value (the fallback). */
  readonly servedBy: string;
}

export type MissingKeyTelemetry = (event: MissingKeyEvent) => void;

export interface CreateI18nOptions {
  readonly lng?: LocaleId | "pseudo";
  /** Override the shipped catalogs (used to inject the pseudo pack). */
  readonly resources?: Partial<Record<LocaleId | "pseudo", Catalog>>;
  readonly onMissingKey?: MissingKeyTelemetry;
}

const BASE_RESOURCES: Record<string, { translation: Catalog }> = {
  "fa-IR": { translation: faIR },
  en: { translation: en },
};

/**
 * Build an isolated i18next instance. Callers own the lifecycle; nothing here is
 * global. `translate()` is the telemetry-aware read path used by consumers.
 */
export function createI18n(options: CreateI18nOptions = {}): I18n {
  const { lng = DEFAULT_LOCALE, resources, onMissingKey } = options;

  const merged: Record<string, { translation: Catalog }> = { ...BASE_RESOURCES };
  if (resources) {
    for (const [id, catalog] of Object.entries(resources)) {
      if (catalog) merged[id] = { translation: catalog };
    }
  }

  const instance = i18next.createInstance();
  instance.use(ICU).init({
    lng,
    fallbackLng: FALLBACK_LOCALE,
    resources: merged,
    interpolation: { escapeValue: false },
    returnNull: false,
    // A truly-unknown key (should be impossible: catalogs are TS-complete) is
    // still reported rather than silently rendered as its raw key.
    saveMissing: Boolean(onMissingKey),
    missingKeyHandler: onMissingKey
      ? (_lngs, _ns, key) =>
          onMissingKey({ key, requested: String(instance.resolvedLanguage), servedBy: "none" })
      : undefined,
  });

  return instance;
}

/**
 * Telemetry-aware translation (LOC-004). Resolves through i18next (so a fa-IR
 * gap falls back to en), and emits a missing-key event when the active locale
 * did not itself contain the key. Never returns a raw key.
 */
export function translate(
  instance: I18n,
  key: MessageKey,
  options?: Record<string, unknown>,
  telemetry?: MissingKeyTelemetry,
): string {
  const active = instance.resolvedLanguage ?? instance.language;
  // `exists` walks the fallback chain, so check the ACTIVE locale's own resource
  // to detect that the value was served by the fallback (LOC-004).
  const inActiveLocale = instance.getResource(active, "translation", key);
  if (telemetry && active !== FALLBACK_LOCALE && inActiveLocale === undefined) {
    telemetry({ key, requested: active, servedBy: FALLBACK_LOCALE });
  }
  return instance.t(key, options ?? {});
}

/** The direction for a locale — read from data, never branched in a view. */
export function directionOf(locale: LocaleId): "rtl" | "ltr" {
  return LOCALE_PACKS[locale].dir;
}
