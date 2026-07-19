import { formatBasisPoints, type MessageKey } from "@market-ops/locale";
import { useLocale, useT } from "../app/i18n";
import { ageMinutes } from "../data/format";
import { freshnessStateFromAge } from "../data/freshness";
import type { EventRankFactors, EventType, MarketEvent, QualityState } from "../data/types";
import { AppLink } from "./AppLink";
import { type EventType as BadgeType, EventTypeBadge, FreshnessPill, QualityBadge } from "./badges";
import { LtrToken } from "./LtrToken";
import { MoneyView } from "./MoneyView";

// EventRow (component inventory): one ranked Today row. Rank strip + body (type,
// LTR native id, quality + freshness, all THREE ranking factors) + an action OR
// blocked panel + a rationale footer. Exposure is the core's EventExposure — a
// known Money amount or explicitly Unknown, never a fabricated number (EVT-005).
// Actionability is derived ONLY from the observed evidence quality: verified /
// supported may lead to a recommendation; every other quality routes to its
// blocker per the IA deep-link map (never silently actionable).

const EVENT_TYPE_NUM: Record<EventType, BadgeType> = {
  winning_state: 1,
  competitor_price: 2,
  seller_count: 3,
  suppression_boundary: 4,
  contribution_floor: 5,
};

// Non-actionable qualities → their blocker copy + deep-link target (IA map).
const BLOCKER: Record<
  Exclude<QualityState, "verified" | "supported">,
  { reasonKey: MessageKey; to: string; withVariant: boolean }
> = {
  unverified: { reasonKey: "today.blocked.reason.unverified", to: "/product", withVariant: true },
  conflicted: {
    reasonKey: "today.blocked.reason.conflicted",
    to: "/operations",
    withVariant: false,
  },
  stale: { reasonKey: "today.blocked.reason.stale", to: "/market", withVariant: false },
  unavailable: {
    reasonKey: "today.blocked.reason.unavailable",
    to: "/product",
    withVariant: true,
  },
};

function isActionable(quality: QualityState): boolean {
  return quality === "verified" || quality === "supported";
}

export function EventRow({
  rank,
  event,
  factors,
}: {
  rank: number;
  event: MarketEvent;
  factors: EventRankFactors;
}) {
  const t = useT();
  const { locale } = useLocale();
  const quality = event.evidenceQuality as QualityState;
  const actionable = isActionable(quality);
  const blocker = actionable ? null : BLOCKER[quality as keyof typeof BLOCKER];

  return (
    <li className="event-row" data-testid="event-row">
      <div className="event-row__rank">
        <span className="event-row__rank-num">{rank}</span>
      </div>

      <div className="event-row__body">
        <div className="event-row__head">
          <EventTypeBadge type={EVENT_TYPE_NUM[event.type]} />
          <LtrToken text={String(event.variantId)} />
          <QualityBadge state={quality} />
          <FreshnessPill state={freshnessStateFromAge(ageMinutes(event.lastEvidenceAt))} />
        </div>

        <dl className="event-row__factors">
          <div className="event-row__factor">
            <dt>{t("today.factor.exposure")}</dt>
            <dd>
              {event.factors.exposure.known && event.factors.exposure.amount ? (
                <MoneyView amount={event.factors.exposure.amount} />
              ) : (
                <span className="muted">{t("today.exposureUnknown")}</span>
              )}
            </dd>
          </div>
          <div className="event-row__factor">
            <dt>{t("today.factor.confidence")}</dt>
            <dd>{formatBasisPoints(factors.confidenceBp, locale)}</dd>
          </div>
          <div className="event-row__factor">
            <dt>{t("today.factor.urgency")}</dt>
            <dd>{formatBasisPoints(factors.urgencyBp, locale)}</dd>
          </div>
        </dl>

        <p className="event-row__rationale">
          <span className="event-row__rationale-q">{t("today.rationale")}</span>{" "}
          <span className="muted">{t("today.rationale.body")}</span>
        </p>
      </div>

      <div className="event-row__action">
        {actionable ? (
          <AppLink
            to="/event"
            search={{ eventId: event.id }}
            className="btn btn--primary"
            testId="event-review"
          >
            {t("today.action.review")}
          </AppLink>
        ) : blocker ? (
          <div className="event-row__blocked" data-testid="event-blocked">
            <span className="sm-state" data-tone="risk">
              <span className="badge__dot" aria-hidden />
              {t("state.blocked")}
            </span>
            <p className="blocker-note">{t(blocker.reasonKey)}</p>
            <AppLink
              to={blocker.to}
              search={blocker.withVariant ? { variantId: event.variantId } : undefined}
              className="btn btn--sm"
            >
              {t("today.action.reviewBlocker")}
            </AppLink>
          </div>
        ) : null}
      </div>
    </li>
  );
}
