import type { MessageKey } from "@market-ops/locale";
import { useLocale, useT } from "../../app/i18n";
import { AppLink } from "../../components/AppLink";
import { ViewState } from "../../components/ViewState";
import { formatCount, formatInstant } from "../../data/format";
import { briefingEventTypeKey } from "../catalogMaps";
import { useBriefing, useLatestBriefingBefore, utcBusinessDay } from "../hooks";
import type { BriefingEvent } from "../types";

// Pre-loaded daily briefing (CHAT-010): a READ whose events carry the SAME ids +
// order as the Today feed. §16 briefing-failure: on error show a failure state —
// Today stays current. Ranks/counts render in the locale's digit family.
//
// Provenance (evidence-quality never-cut, #119): a FAILED fetch must NEVER present
// the requested business day as a "last briefing" date. Error ≠ absence (the #81 /
// #295 pattern): a request date is not observed history. No authoritative prior
// briefing is available on this surface (the /briefing read is single-day, with no
// latest-success metadata or persisted client cache), so the failure state uses
// the additive `/briefing/latest` provenance read. Only its stored briefing may
// supply a date; never-generated and lookup-error states carry NO date.

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
      {/* LOC-002 (#121): the machine `eventType` maps to a CLOSED catalog label;
          an unmapped type renders the localized unavailable label + drift
          telemetry — the raw value is never shown. Independent of severity. */}
      <span className="briefing__eventType" data-testid="briefing-eventType">
        {t(briefingEventTypeKey(event.eventType))}
      </span>
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
  const latest = useLatestBriefingBefore(businessDay, query.isError);

  if (query.isError) {
    const retry = () => {
      void query.refetch();
      void latest.refetch();
    };
    return (
      <section className="briefing briefing--failed" data-testid="briefing-failure">
        <p className="briefing__title">{t("chat.briefing.failure.title")}</p>
        <ViewState pending={latest.isPending} error={false}>
          {latest.data?.state === "available" && latest.data.briefing ? (
            <p className="briefing__failure-body" data-testid="briefing-failure-known">
              {t("chat.briefing.failure.lastSuccessful", {
                date: formatInstant(latest.data.briefing.generatedAt, locale),
              })}
            </p>
          ) : (
            <p
              className="briefing__failure-body"
              data-testid="briefing-failure-unknown"
              data-provenance-state={latest.isError ? "unavailable" : "never-generated"}
            >
              {t(
                latest.isError
                  ? "chat.briefing.failure.lookupUnavailable"
                  : "chat.briefing.failure.neverGenerated",
              )}
            </p>
          )}
        </ViewState>
        {!latest.isPending ? (
          <button
            type="button"
            className="btn btn--secondary"
            disabled={query.isFetching || latest.isFetching}
            onClick={retry}
          >
            {t("action.retry")}
          </button>
        ) : null}
      </section>
    );
  }

  return (
    <section className="briefing" data-testid="briefing">
      <p className="briefing__title">{t("chat.briefing.title")}</p>
      <ViewState pending={query.isPending} error={false}>
        {query.data && query.data.events.length > 0 ? (
          <>
            <p className="briefing__generatedAt" data-testid="briefing-generatedAt">
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
