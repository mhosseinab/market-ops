import {
  buildPseudoCatalog,
  createI18n,
  LOCALE_PACKS,
  type LocaleId,
  type MessageKey,
  type MissingKeyEvent,
  PSEUDO_DIR,
  PSEUDO_ID,
  translate,
} from "@market-ops/locale";
import type { i18n as I18n } from "i18next";
import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from "react";
import { I18nextProvider, useTranslation } from "react-i18next";
import {
  consoleMissingKeySink,
  createMissingKeyReporter,
  type MissingKeySink,
} from "./missingKeyTelemetry";

// The single i18n seam for the app. It owns: the i18next instance (ICU catalogs
// from the locale pack), driving `dir`/`lang` onto <html> from locale DATA
// (LOC-005, no direction branch in a view), and a telemetry-aware `useT` that
// emits a missing-key event on any fallback (LOC-004).

// The application telemetry sink for production missing-key fallbacks. It is an
// injectable seam (not a hard-coded global): a deployment or a test can swap it
// via `setMissingKeySink`. The default is the structured, bounded prod sink.
//
// One bounded, failure-safe reporter forwards to the active sink; its dedup
// state is rebuilt whenever the sink is swapped, so swapping the telemetry
// backend starts a fresh dedup window (and keeps tests isolated).
let emitMissingKey = createMissingKeyReporter(consoleMissingKeySink);

/** Inject a telemetry sink (deployment wiring / tests). */
export function setMissingKeySink(sink: MissingKeySink): void {
  emitMissingKey = createMissingKeyReporter(sink);
}

/** Restore the default production sink. */
export function resetMissingKeySink(): void {
  emitMissingKey = createMissingKeyReporter(consoleMissingKeySink);
}

export function reportMissingKey(event: MissingKeyEvent): void {
  if (import.meta.env.DEV) {
    // Dev breadcrumb: a raw, un-deduped warning is the useful local signal.
    console.warn("[i18n] missing key served by fallback", event);
    return;
  }
  // Production (issue #14): the fallback is invisible unless we emit. This path
  // is bounded and failure-safe; it never renders a raw key (the fallback
  // already served a value) and never carries rendered copy.
  emitMissingKey(event);
}

interface LocaleState {
  readonly locale: LocaleId;
  setLocale: (next: LocaleId) => void;
}

// Exported so test harnesses (e.g. the pseudo-locale harness) can satisfy
// `useLocale` for components that format counts/dates while a non-app i18next
// instance serves the copy. Production always goes through I18nProvider.
export const LocaleContext = createContext<LocaleState | null>(null);

export function I18nProvider({
  children,
  initialLocale,
  pseudo = false,
}: {
  children: ReactNode;
  initialLocale: LocaleId;
  /**
   * Pseudo-localization mode (LOC-011): the copy is served from the generated
   * pseudo pack (expanded + bracketed + forced-LTR) and the document root is
   * driven to `PSEUDO_DIR`, so the browser layout gate can render the real shell
   * under the pseudo direction. Formatters still use `initialLocale` (the pseudo
   * pack only affects `t()`), matching production's data-driven boundary. Off in
   * production — the base-pack path below is byte-for-byte unchanged.
   */
  pseudo?: boolean;
}) {
  const [locale, setLocale] = useState<LocaleId>(initialLocale);
  const [instance] = useState<I18n>(() =>
    pseudo
      ? createI18n({ lng: PSEUDO_ID, resources: { [PSEUDO_ID]: buildPseudoCatalog() } })
      : createI18n({ lng: initialLocale }),
  );

  useEffect(() => {
    // Direction/lang are DATA, never branched in a view: the base packs carry
    // their own dir, and the pseudo pack's direction is the exported PSEUDO_DIR.
    if (pseudo) {
      document.documentElement.setAttribute("dir", PSEUDO_DIR);
      document.documentElement.setAttribute("lang", PSEUDO_ID);
      return;
    }
    const pack = LOCALE_PACKS[locale];
    document.documentElement.setAttribute("dir", pack.dir);
    document.documentElement.setAttribute("lang", pack.lang);
    void instance.changeLanguage(locale);
  }, [locale, instance, pseudo]);

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
