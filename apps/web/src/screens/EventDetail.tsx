import { formatBasisPoints, type MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useLocale, useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import {
  type EventType as BadgeType,
  EventTypeBadge,
  FreshnessPill,
  QualityBadge,
} from "../components/badges";
import { EvidencePanel } from "../components/EvidencePanel";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { ageMinutes, formatInstant } from "../data/format";
import { useEvent } from "../data/hooks";
import type { EventLifecycleState, EventSeverity, EventType, QualityState } from "../data/types";

// Event detail (design screen 4 / EVT-001): the market event with its lifecycle,
// versioned materiality-threshold provenance (EVT-002), all three ranking factors,
// exposure (known Money or explicitly Unknown, EVT-005), and the four-way evidence
// separation. Observed facts, DK signals, seller config, and model inference are
// kept visually distinct — inference is never presented as an observed fact.

const EVENT_TYPE_NUM: Record<EventType, BadgeType> = {
  winning_state: 1,
  competitor_price: 2,
  seller_count: 3,
  suppression_boundary: 4,
  contribution_floor: 5,
};

const SEVERITY_LABEL: Record<EventSeverity, MessageKey> = {
  info: "event.severity.info",
  warning: "event.severity.warning",
  critical: "event.severity.critical",
};

const LIFECYCLE_LABEL: Record<EventLifecycleState, MessageKey> = {
  open: "event.lifecycle.open",
  updated: "event.lifecycle.updated",
  resolved: "event.lifecycle.resolved",
  expired: "event.lifecycle.expired",
};

export function EventDetail() {
  const t = useT();
  const { locale } = useLocale();
  const eventId = useRouterState({
    select: (s) => (s.location.search as { eventId?: string }).eventId,
  });
  const eventQuery = useEvent(eventId);
  const event = eventQuery.data;

  return (
    <div className="screen">
      <div className="toolbar">
        <AppLink to="/today" className="link">
          {t("action.back")}
        </AppLink>
        {eventId ? <LtrToken text={eventId} /> : null}
      </div>

      <ViewState
        pending={Boolean(eventId) && eventQuery.isPending}
        error={eventQuery.isError}
        onRetry={() => void eventQuery.refetch()}
      >
        {!eventId || !event ? (
          <div className="screen-empty">
            <p>{t("event.notFound")}</p>
          </div>
        ) : (
          <>
            <div className="event-detail__head">
              <EventTypeBadge type={EVENT_TYPE_NUM[event.type]} />
              <QualityBadge state={event.evidenceQuality as QualityState} />
              <FreshnessPill ageMinutes={ageMinutes(event.lastEvidenceAt)} />
              <LtrToken text={String(event.variantId)} />
            </div>

            <div className="screen__grid">
              <Section titleKey="event.section.factors">
                <dl className="kv">
                  <div className="kv__row">
                    <dt>{t("today.factor.exposure")}</dt>
                    <dd>
                      {event.factors.exposure.known && event.factors.exposure.amount ? (
                        <MoneyView amount={event.factors.exposure.amount} />
                      ) : (
                        <span className="muted">{t("today.exposureUnknown")}</span>
                      )}
                    </dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("today.factor.confidence")}</dt>
                    <dd>{formatBasisPoints(event.factors.confidenceBp, locale)}</dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("today.factor.urgency")}</dt>
                    <dd>{formatBasisPoints(event.factors.urgencyBp, locale)}</dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("event.severity.info")}</dt>
                    <dd>{t(SEVERITY_LABEL[event.severity])}</dd>
                  </div>
                </dl>
              </Section>

              <Section titleKey="event.section.lifecycle">
                <dl className="kv">
                  <div className="kv__row">
                    <dt>{t("event.section.lifecycle")}</dt>
                    <dd>{t(LIFECYCLE_LABEL[event.state])}</dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("event.firstDetected")}</dt>
                    <dd>{formatInstant(event.firstDetectedAt, locale)}</dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("event.lastEvidence")}</dt>
                    <dd>{formatInstant(event.lastEvidenceAt, locale)}</dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("event.expires")}</dt>
                    <dd>{formatInstant(event.expiresAt, locale)}</dd>
                  </div>
                  <div className="kv__row">
                    <dt>{t("event.updateCount", { count: event.evidenceUpdateCount })}</dt>
                    <dd />
                  </div>
                </dl>
              </Section>
            </div>

            <Section titleKey="event.section.evidence">
              <div className="evidence-grid">
                <EvidencePanel kind="observed">
                  <dl className="kv">
                    <div className="kv__row">
                      <dt>{t("event.evidenceRef")}</dt>
                      <dd>
                        {event.evidenceRef ? (
                          <LtrToken text={event.evidenceRef} />
                        ) : (
                          <span className="muted">{t("common.notAvailable")}</span>
                        )}
                      </dd>
                    </div>
                    <div className="kv__row">
                      <dt>{t("rec.field.quality")}</dt>
                      <dd>
                        <QualityBadge state={event.evidenceQuality as QualityState} />
                      </dd>
                    </div>
                  </dl>
                </EvidencePanel>

                <EvidencePanel kind="dk">
                  <p className="muted">
                    {typeof event.thresholdVersion === "number"
                      ? t("event.threshold", { version: event.thresholdVersion })
                      : t("common.notAvailable")}
                  </p>
                </EvidencePanel>

                <EvidencePanel kind="config">
                  <p className="muted">{t("common.notAvailable")}</p>
                </EvidencePanel>

                <EvidencePanel kind="inference">
                  <p className="muted">{t("today.rationale.body")}</p>
                </EvidencePanel>
              </div>
            </Section>

            <div className="toolbar">
              <AppLink
                to="/recommendation"
                search={{ variantId: event.variantId }}
                className="btn btn--primary"
                testId="event-to-recommendation"
              >
                {t("event.cta.recommendation")}
              </AppLink>
            </div>
          </>
        )}
      </ViewState>
    </div>
  );
}
