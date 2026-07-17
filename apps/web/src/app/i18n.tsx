import {
  createI18n,
  LOCALE_PACKS,
  type LocaleId,
  type MessageKey,
  type MissingKeyEvent,
  translate,
} from "@market-ops/locale";
import type { i18n as I18n } from "i18next";
import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from "react";
import { I18nextProvider, useTranslation } from "react-i18next";

// The single i18n seam for the app. It owns: the i18next instance (ICU catalogs
// from the locale pack), driving `dir`/`lang` onto <html> from locale DATA
// (LOC-005, no direction branch in a view), and a telemetry-aware `useT` that
// emits a missing-key event on any fallback (LOC-004).

function reportMissingKey(event: MissingKeyEvent): void {
  // Dev-time telemetry sink. In prod this is where an observability breadcrumb
  // would be emitted; it is never a raw-key render (the fallback served a value).
  if (import.meta.env.DEV) {
    console.warn("[i18n] missing key served by fallback", event);
  }
}

interface LocaleState {
  readonly locale: LocaleId;
  setLocale: (next: LocaleId) => void;
}

const LocaleContext = createContext<LocaleState | null>(null);

export function I18nProvider({
  children,
  initialLocale,
}: {
  children: ReactNode;
  initialLocale: LocaleId;
}) {
  const [locale, setLocale] = useState<LocaleId>(initialLocale);
  const [instance] = useState<I18n>(() => createI18n({ lng: initialLocale }));

  useEffect(() => {
    const pack = LOCALE_PACKS[locale];
    document.documentElement.setAttribute("dir", pack.dir);
    document.documentElement.setAttribute("lang", pack.lang);
    void instance.changeLanguage(locale);
  }, [locale, instance]);

  const value = useMemo<LocaleState>(() => ({ locale, setLocale }), [locale]);

  return (
    <LocaleContext.Provider value={value}>
      <I18nextProvider i18n={instance}>{children}</I18nextProvider>
    </LocaleContext.Provider>
  );
}

export function useLocale(): LocaleState {
  const ctx = useContext(LocaleContext);
  if (!ctx) throw new Error("useLocale must be used within I18nProvider");
  return ctx;
}

/** Telemetry-aware translation bound to the closed `MessageKey` set. */
export function useT(): (key: MessageKey, vars?: Record<string, unknown>) => string {
  const { i18n } = useTranslation();
  return useMemo(
    () => (key: MessageKey, vars?: Record<string, unknown>) =>
      translate(i18n, key, vars, reportMissingKey),
    [i18n],
  );
}
