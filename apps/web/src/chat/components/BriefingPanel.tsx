import type { MessageKey } from "@market-ops/locale";
import { useLocale, useT } from "../../app/i18n";
import { AppLink } from "../../components/AppLink";
import { LtrToken } from "../../components/LtrToken";
import { ViewState } from "../../components/ViewState";
import { formatCount, formatInstant } from "../../data/format";
import { useBriefing, utcBusinessDay } from "../hooks";
import type { BriefingEvent } from "../types";

// Pre-loaded daily briefing (CHAT-010): a READ whose events carry the SAME ids +
// order as the Today feed. §16 briefing-failure: on error show the dated
// last-briefing failure state — Today stays current. Ranks/counts render in the
// locale's digit family.

const SEVERITY_KEY: Record<string, MessageKey> = {
  info: "event.severity.info",
  warning: "event.severity.warning",
  critical: "event.severity.critical",
};

function BriefingRow({ event }: { event: BriefingEvent }) {
  const t = useT();
  const { locale } = useLocale();
  const severityKey = SEVERITY_KEY[event.severity];
  return (
    <li className="briefing__row" data-testid="briefing-row">
      <span className="briefing__rank">
        {t("chat.briefing.rank", { rank: formatCount(event.rank, locale) })}
      </span>
      <LtrToken text={event.eventType} />
      {severityKey ? <span className="briefing__severity">{t(severityKey)}</span> : null}
      <AppLink
        to="/event"
        search={{ eventId: event.eventId }}
        className="chat-deeplink"
        testId="briefing-open"
      >
        {t("chat.briefing.open")}
      </AppLink>
    </li>
  );
}

export function BriefingPanel() {
  const t = useT();
  const { locale } = useLocale();
  const businessDay = utcBusinessDay();
  const query = useBriefing(businessDay);

  if (query.isError) {
    // §16 briefing failure: dated last-briefing + failure state; Today unaffected.
    return (
      <section className="briefing briefing--failed" data-testid="briefing-failure">
        <p className="briefing__title">{t("chat.briefing.failure.title")}</p>
        <p className="briefing__failure-body">
          {t("chat.briefing.failure.body", {
            date: formatInstant(`${businessDay}T00:00:00Z`, locale),
          })}
        </p>
      </section>
    );
  }

  return (
    <section className="briefing" data-testid="briefing">
      <p className="briefing__title">{t("chat.briefing.title")}</p>
      <ViewState pending={query.isPending} error={false}>
        {query.data && query.data.events.length > 0 ? (
          <>
            <p className="briefing__generatedAt">
              {t("chat.briefing.generatedAt", {
                time: formatInstant(query.data.generatedAt, locale),
              })}
            </p>
            <ul className="briefing__rows">
              {query.data.events.map((event) => (
                <BriefingRow key={event.eventId} event={event} />
              ))}
            </ul>
          </>
        ) : (
          <p className="briefing__empty">{t("chat.briefing.empty")}</p>
        )}
      </ViewState>
    </section>
  );
}
