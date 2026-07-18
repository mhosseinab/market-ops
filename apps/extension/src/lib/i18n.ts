import { createI18n, DEFAULT_LOCALE, type MessageKey, translate } from "@market-ops/locale";

// The extension's copy surface is the SAME locale pack the SPA uses (LOC
// boundary, PRD §11) — zero string literals in the popup/overlay, catalog keys
// with named ICU slots only. fa-IR is the shipping P0 locale (DEFAULT_LOCALE);
// the extension does not offer a locale switcher (P0 scope).
const instance = createI18n({ lng: DEFAULT_LOCALE });

export function t(key: MessageKey, vars?: Record<string, unknown>): string {
  return translate(instance, key, vars);
}

export const EXT_DIR = "rtl" as const;
export const EXT_LANG = "fa" as const;
