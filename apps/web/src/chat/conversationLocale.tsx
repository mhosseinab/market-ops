import { createI18n, LOCALE_PACKS, type LocaleId } from "@market-ops/locale";
import type { i18n as I18n } from "i18next";
import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useMemo,
  useRef,
  useState,
} from "react";
import { flushSync } from "react-dom";
import { I18nextProvider } from "react-i18next";
import { LocaleContext } from "../app/i18n";

export interface PreparedConversationLocale {
  readonly locale: LocaleId;
  readonly instance: I18n;
}

export type ConversationLocalePreparer = (locale: LocaleId) => Promise<PreparedConversationLocale>;

export interface ConversationLocaleController extends PreparedConversationLocale {
  activate: (locale: unknown) => Promise<void>;
}

function isSupportedLocale(locale: unknown): locale is LocaleId {
  return typeof locale === "string" && Object.hasOwn(LOCALE_PACKS, locale);
}

function prepareLocale(locale: LocaleId): PreparedConversationLocale {
  const instance = createI18n({ lng: locale });
  if (instance.resolvedLanguage !== locale) {
    throw new Error("chat_locale_catalog_unavailable");
  }
  return { locale, instance };
}

const DEFAULT_PREPARER: ConversationLocalePreparer = async (locale) => prepareLocale(locale);
const ConversationLocalePreparerContext =
  createContext<ConversationLocalePreparer>(DEFAULT_PREPARER);

export function ConversationLocalePreparerProvider({
  prepare,
  children,
}: {
  prepare: ConversationLocalePreparer;
  children: ReactNode;
}) {
  return (
    <ConversationLocalePreparerContext.Provider value={prepare}>
      {children}
    </ConversationLocalePreparerContext.Provider>
  );
}

/**
 * Owns the catalog used by the conversation surface. It is deliberately
 * independent from the application locale preference: a gateway-echoed locale
 * can switch chat copy without changing the rest of the shell or <html> direction.
 */
export function useConversationLocale(initialLocale: LocaleId): ConversationLocaleController {
  const prepare = useContext(ConversationLocalePreparerContext);
  const [prepared, setPrepared] = useState<PreparedConversationLocale>(() =>
    prepareLocale(initialLocale),
  );
  const preparedRef = useRef(prepared);
  preparedRef.current = prepared;

  const activate = useCallback(
    async (locale: unknown) => {
      if (!isSupportedLocale(locale)) throw new Error("chat_locale_unsupported");
      if (preparedRef.current.locale === locale) return;

      const next = await prepare(locale);
      if (next.locale !== locale || next.instance.resolvedLanguage !== locale) {
        throw new Error("chat_locale_catalog_unavailable");
      }
      // Commit the prepared catalog before the stream consumer may expose a token
      // or terminal frame. flushSync makes that ordering observable at the DOM
      // boundary instead of relying on React's async batching chronology.
      flushSync(() => {
        preparedRef.current = next;
        setPrepared(next);
      });
    },
    [prepare],
  );

  return { ...prepared, activate };
}

export function ConversationLocaleBoundary({
  controller,
  children,
}: {
  controller: ConversationLocaleController;
  children: ReactNode;
}) {
  const localeState = useMemo(
    () => ({
      locale: controller.locale,
      setLocale: (next: LocaleId) => {
        void controller.activate(next);
      },
    }),
    [controller],
  );

  return (
    <LocaleContext.Provider value={localeState}>
      <I18nextProvider i18n={controller.instance}>{children}</I18nextProvider>
    </LocaleContext.Provider>
  );
}
