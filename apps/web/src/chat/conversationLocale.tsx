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
import { I18nextProvider, useTranslation } from "react-i18next";
import { LocaleContext } from "../app/i18n";

export interface PreparedConversationLocale {
  readonly locale: LocaleId;
  readonly instance: I18n;
}

export type ConversationLocalePreparer = (locale: LocaleId) => Promise<PreparedConversationLocale>;

export interface ConversationLocaleController extends PreparedConversationLocale {
  readonly dir: string;
  readonly lang: string;
  activate: (locale: unknown) => Promise<void>;
  reset: () => void;
  setApplicationLocale: (locale: LocaleId) => void;
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
 * Before a gateway binding exists, the conversation surface shares the active
 * application i18next instance. That keeps a closed/unbound dock synchronized
 * with application language changes and preserves non-gateway catalogs such as
 * the pseudo-locale. Only an authoritative gateway frame creates an isolated,
 * supported-locale catalog; subsequent app changes cannot relabel that binding.
 */
export function useConversationLocale(
  applicationLocale: LocaleId,
  setApplicationLocale: (locale: LocaleId) => void,
): ConversationLocaleController {
  const prepare = useContext(ConversationLocalePreparerContext);
  const { i18n: applicationInstance } = useTranslation();
  const [bound, setBound] = useState<PreparedConversationLocale | null>(null);
  const boundRef = useRef(bound);
  boundRef.current = bound;

  const activate = useCallback(
    async (locale: unknown) => {
      if (!isSupportedLocale(locale)) throw new Error("chat_locale_unsupported");
      if (boundRef.current?.locale === locale) return;

      const next = await prepare(locale);
      if (next.locale !== locale || next.instance.resolvedLanguage !== locale) {
        throw new Error("chat_locale_catalog_unavailable");
      }
      // Commit the prepared catalog before the stream consumer may expose a token
      // or terminal frame. flushSync makes that ordering observable at the DOM
      // boundary instead of relying on React's async batching chronology.
      flushSync(() => {
        boundRef.current = next;
        setBound(next);
      });
    },
    [prepare],
  );

  const reset = useCallback(() => {
    if (boundRef.current === null) return;
    flushSync(() => {
      boundRef.current = null;
      setBound(null);
    });
  }, []);

  const locale = bound?.locale ?? applicationLocale;
  const instance = bound?.instance ?? applicationInstance;
  const catalogLanguage = instance.resolvedLanguage ?? instance.language;
  const supportedCatalog = isSupportedLocale(catalogLanguage);
  const pack = supportedCatalog ? LOCALE_PACKS[catalogLanguage] : null;

  return {
    locale,
    instance,
    dir: pack?.dir ?? instance.dir(catalogLanguage),
    lang: pack?.lang ?? catalogLanguage,
    activate,
    reset,
    setApplicationLocale,
  };
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
      setLocale: controller.setApplicationLocale,
    }),
    [controller],
  );

  return (
    <LocaleContext.Provider value={localeState}>
      <I18nextProvider i18n={controller.instance}>{children}</I18nextProvider>
    </LocaleContext.Provider>
  );
}
