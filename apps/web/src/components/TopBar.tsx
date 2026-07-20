import { useRouter, useRouterState } from "@tanstack/react-router";
import { useAppState } from "../app/appState";
import { useLocale, useT } from "../app/i18n";
import { DEFAULT_ROUTE, ROUTES } from "../app/navConfig";
import { deriveConnectorHealth } from "../data/connectorHealth";
import { useConnectorStatus, useLogout } from "../data/hooks";
import { ConnectorHealthPill } from "./ConnectorHealthPill";

// Top bar: route title/subtitle, connection-health pill, density/theme/chat
// toggles, and the language switch that flips the whole app (demonstrates the
// locale engine — LOCALIZATION.md). All control labels are aria-labelled through
// the catalog; visible glyphs are non-linguistic.
export function TopBar() {
  const t = useT();
  const { locale, setLocale } = useLocale();
  const { toggleTheme, toggleDensity, toggleChat, briefingUnseen } = useAppState();
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const route = ROUTES.find((r) => r.path === pathname) ?? DEFAULT_ROUTE;

  // Connector health is DERIVED from the current typed connector state and fails
  // closed (issue #18): while the status is pending/errored the data is absent,
  // so the shared rule resolves to a non-positive health rather than a stale
  // "healthy" pill. The pill NEVER reads positive unless a probe confirmed it.
  const connectorQuery = useConnectorStatus();
  const health = deriveConnectorHealth(connectorQuery.data);

  // Logout (issue #168): close the server-side session, then route to login. The
  // logout mutation clears the protected Query cache on success so no
  // account-scoped data outlives the session; navigation follows once cleared.
  const router = useRouter();
  const logout = useLogout();
  const onLogout = () =>
    logout.mutate(undefined, {
      onSuccess: () => {
        void router.navigate({ to: "/login" });
      },
    });

  return (
    <header className="top-bar">
      <div className="top-bar__titles">
        <span className="top-bar__title">{t(route.titleKey)}</span>
        <span className="top-bar__sub">{t(route.subKey)}</span>
      </div>

      <ConnectorHealthPill health={health} />

      <button
        type="button"
        className="top-bar__control"
        onClick={() => setLocale(locale === "fa-IR" ? "en" : "fa-IR")}
      >
        {t(locale === "fa-IR" ? "app.langName.en" : "app.langName.fa")}
      </button>

      <button
        type="button"
        className="top-bar__control"
        aria-label={t("topbar.density.toggle")}
        onClick={toggleDensity}
      >
        {"▤"}
      </button>
      <button
        type="button"
        className="top-bar__control"
        aria-label={t("topbar.theme.toggle")}
        onClick={toggleTheme}
      >
        {"☾"}
      </button>
      <button
        type="button"
        className="top-bar__control"
        aria-label={t("topbar.chat.toggle")}
        onClick={toggleChat}
      >
        {"◧"}
        {briefingUnseen && <span className="briefing-dot" aria-hidden />}
      </button>
      <button
        type="button"
        className="top-bar__control"
        data-testid="logout"
        disabled={logout.isPending}
        onClick={onLogout}
      >
        {t("auth.logout")}
      </button>
    </header>
  );
}
