import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useLocale, useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import { Banner } from "../components/Banner";
import {
  AvailabilityBadge,
  FreshnessPill,
  QualityBadge,
  ReadinessBadge,
} from "../components/badges";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount, formatInstant } from "../data/format";
import { freshnessState } from "../data/freshness";
import {
  useCostProfiles,
  useMarginReadiness,
  useObservations,
  useObservationTargets,
  useObservedOffers,
} from "../data/hooks";
import type { CostComponent, CostProfileVersion } from "../data/types";

const COST_COMPONENTS: readonly CostComponent[] = [
  "cogs",
  "commission",
  "fulfillment",
  "shipping",
  "packaging",
  "promotion",
  "ads",
  "returns",
];

const COMPONENT_LABEL: Record<CostComponent, MessageKey> = {
  cogs: "costComponent.cogs",
  commission: "costComponent.commission",
  fulfillment: "costComponent.fulfillment",
  shipping: "costComponent.shipping",
  packaging: "costComponent.packaging",
  promotion: "costComponent.promotion",
  ads: "costComponent.ads",
  returns: "costComponent.returns",
};

// Product detail (design screen 8). Owned offer is explicitly unavailable (no
// owned-offer capability yet — never fabricated); market snapshot, stock, and
// versioned cost profile render observed/derived data; contribution is a
// placeholder until readiness is Complete (only Complete is executable);
// diagnostics is read-only and names the observed field + rule (parser) version
// from the newest observation evidence.
export function ProductDetail() {
  const t = useT();
  const { locale } = useLocale();
  const variantId = useRouterState({
    select: (s) => (s.location.search as { variantId?: string }).variantId,
  });

  const targetsQuery = useObservationTargets();
  const offersQuery = useObservedOffers();
  const readinessQuery = useMarginReadiness(variantId);
  const profilesQuery = useCostProfiles(variantId);

  const target = targetsQuery.data?.items.find((tg) => tg.variantId === variantId);
  const offer = offersQuery.data?.items.find((o) => o.targetId === target?.id);
  const observationsQuery = useObservations(target?.id);
  const latestObservation = observationsQuery.data?.items[0];
  const readiness = readinessQuery.data;
  const profileByComponent = new Map<CostComponent, CostProfileVersion>();
  for (const p of profilesQuery.data?.items ?? []) profileByComponent.set(p.component, p);

  return (
    <div className="screen">
      <div className="toolbar">
        <AppLink to="/products" className="link">
          {t("product.back")}
        </AppLink>
        {/* Native product ID is a technical identifier: raw + LTR-isolated. */}
        {target ? <LtrToken text={String(target.nativeProductId)} /> : null}
      </div>

      <ViewState
        pending={targetsQuery.isPending}
        error={targetsQuery.isError}
        onRetry={() => void targetsQuery.refetch()}
      >
        {!variantId || !target ? (
          <div className="screen-empty">
            <p>{t("product.noTarget")}</p>
          </div>
        ) : (
          <>
            {readiness?.state === "missing" ? (
              <Banner
                tone="risk"
                title={t("readiness.missing")}
                body={t("product.contribution.placeholder")}
                actions={
                  <AppLink to="/cost" search={{ variantId }} className="btn btn--primary">
                    {t("cost.single.submit")}
                  </AppLink>
                }
              />
            ) : null}

            <div className="screen__grid">
              <Section titleKey="product.section.ownedOffer">
                <p className="muted">{t("product.ownedOffer.unavailable")}</p>
              </Section>

              <Section titleKey="product.section.snapshot">
                {offer ? (
                  <div className="kv">
                    <div className="kv__row">
                      <span>{t("product.marketPrice")}</span>
                      <LtrToken text={offer.price.text} />
                    </div>
                    <div className="kv__row">
                      <span>{t("product.listPrice")}</span>
                      <LtrToken text={offer.listPrice.text} />
                    </div>
                    <div className="kv__row">
                      <span>{t("product.section.readiness")}</span>
                      <span className="inline-badges">
                        <QualityBadge state={offer.quality} />
                        <FreshnessPill state={freshnessState(offer, Date.now())} />
                      </span>
                    </div>
                    <div className="kv__row">
                      <span>{t("product.stock.title")}</span>
                      <span className="inline-badges">
                        <AvailabilityBadge state={offer.availabilityStatus} />
                        <span>
                          {typeof offer.stockSignal === "number"
                            ? t("product.stock.signal", {
                                count: formatCount(offer.stockSignal, locale),
                              })
                            : t("product.stock.noSignal")}
                        </span>
                      </span>
                    </div>
                  </div>
                ) : (
                  <p className="muted">{t("common.notAvailable")}</p>
                )}
              </Section>

              <Section titleKey="product.contribution.title">
                {readiness?.state === "complete" ? (
                  <p className="muted">{t("common.notAvailable")}</p>
                ) : (
                  <p className="muted">{t("product.contribution.placeholder")}</p>
                )}
              </Section>

              <Section titleKey="product.section.readiness">
                {readiness ? (
                  <>
                    <ReadinessBadge state={readiness.state} />
                    {readiness.missingComponents.length > 0 ? (
                      <div className="component-list">
                        <span className="component-list__label">
                          {t("product.readiness.missingList")}
                        </span>
                        {readiness.missingComponents.map((c) => (
                          <span key={c} className="chip">
                            {t(COMPONENT_LABEL[c])}
                          </span>
                        ))}
                      </div>
                    ) : null}
                    {readiness.staleComponents.length > 0 ? (
                      <div className="component-list">
                        <span className="component-list__label">
                          {t("product.readiness.staleList")}
                        </span>
                        {readiness.staleComponents.map((c) => (
                          <span key={c} className="chip">
                            {t(COMPONENT_LABEL[c])}
                          </span>
                        ))}
                      </div>
                    ) : null}
                  </>
                ) : (
                  <p className="muted">{t("common.notAvailable")}</p>
                )}
              </Section>
            </div>

            <Section titleKey="product.section.costs">
              <ul className="cost-list">
                {COST_COMPONENTS.map((component) => {
                  const profile = profileByComponent.get(component);
                  return (
                    <li key={component} className="cost-list__item">
                      <span className="cost-list__name">{t(COMPONENT_LABEL[component])}</span>
                      {profile ? (
                        <>
                          <MoneyView amount={profile.amount} />
                          <span className="muted">
                            {t("product.cost.version", { version: profile.version })}
                          </span>
                        </>
                      ) : (
                        <span className="muted">{t("product.cost.notRecorded")}</span>
                      )}
                    </li>
                  );
                })}
              </ul>
            </Section>

            <Section
              titleKey="product.section.diagnostics"
              actions={
                <AppLink to="/diagnostics" search={{ variantId }} className="link">
                  {t("product.diagnostics.viewAll")}
                </AppLink>
              }
            >
              {latestObservation ? (
                <dl className="kv">
                  <div className="kv__row">
                    <span>{t("product.diagnostics.observedField")}</span>
                    <LtrToken text={latestObservation.sourceType} />
                  </div>
                  <div className="kv__row">
                    <span>
                      {t("product.diagnostics.ruleVersion", {
                        version: latestObservation.parserVersion,
                      })}
                    </span>
                  </div>
                  <div className="kv__row">
                    <span>{t("product.diagnostics.evidence")}</span>
                    <LtrToken text={latestObservation.evidenceRef} />
                  </div>
                  <div className="kv__row">
                    <span>{t("product.diagnostics.source")}</span>
                    <span className="muted">
                      {formatInstant(latestObservation.capturedAt, locale)}
                    </span>
                  </div>
                </dl>
              ) : (
                <p className="muted">{t("common.notAvailable")}</p>
              )}
            </Section>
          </>
        )}
      </ViewState>
    </div>
  );
}
