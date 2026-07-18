import { buildPseudoCatalog, createI18n, DEFAULT_LOCALE } from "@market-ops/locale";
import type { ReactNode } from "react";
import { useState } from "react";
import { I18nextProvider } from "react-i18next";
import { LocaleContext } from "../app/i18n";

// Renders children under the generated pseudo pack (expanded + bracketed +
// forced-LTR). Any user-facing text that does NOT pass through the catalog
// (a hardcoded literal, a raw key) will be missing the ⟦…⟧ brackets, which the
// pseudo suite asserts on (LOC-011). A LocaleContext is also provided so
// components that format counts/dates (useLocale) render under the harness; only
// the copy (t()) is asserted, so the underlying digit locale is immaterial.
export function PseudoHarness({ children }: { children: ReactNode }) {
  const [i18n] = useState(() =>
    createI18n({ lng: "pseudo", resources: { pseudo: buildPseudoCatalog() } }),
  );
  return (
    <LocaleContext.Provider value={{ locale: DEFAULT_LOCALE, setLocale: () => {} }}>
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    </LocaleContext.Provider>
  );
}
