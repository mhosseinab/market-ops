import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useLocale, useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import { Banner } from "../components/Banner";
import {
  AvailabilityBadge,
  CapabilityBadge,
  DiagnosticResultBadge,
  FreshnessPill,
  MappingBadge,
  QualityBadge,
  ReadinessBadge,
} from "../components/badges";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { Section } from "../components/primitives";
import { SectionError } from "../components/SectionError";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { freshnessState } from "../data/freshness";
import {
  useCatalogProduct,
  useCostProfiles,
  useMarginReadiness,
  useProductDiagnostics,
} from "../data/hooks";
import type {
  CostComponent,
  CostProfileVersion,
  ListingDiagnosticField,
  ObservedOffer,
  OwnedOfferView,
} from "../data/types";

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

const DIAGNOSTIC_FIELD_LABEL: Record<ListingDiagnosticField, MessageKey> = {
  title: "diagnostics.field.title",
  description: "diagnostics.field.description",
  image: "diagnostics.field.image",
};

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

// The reason an owned offer is not rendered (capability gated or absent) maps to a
// canonical copy key — never free text. `capability_not_supported` covers every
// non-Supported capability state (Unknown never enables the price).
function ownedReasonKey(owned: OwnedOfferView): MessageKey {
  switch (owned.unavailableReason) {
    case "capability_not_supported":
      return "product.ownedOffer.reason.capabilityNotSupported";
    case "no_owned_offer":
      return "product.ownedOffer.reason.noOwnedOffer";
    default:
      return "product.ownedOffer.unavailable";
  }
}

// The deterministic market snapshot for a variant: the first current competitor
// offer by offerIdentity ascending (money quarantine forbids numeric ranking).
function primaryOffer(offers: readonly ObservedOffer[]): ObservedOffer | undefined {
  return offers[0];
}

// Product detail (design screen 8). Sourced from the CANONICAL product row
// (Product/Variant/Owned Offer), NOT an observation target — so a synced but
// unwatched/unmapped variant still renders. The owned offer is capability-gated
// (owned_offer_read, §15.2): its data renders only when Supported, otherwise a
// reason is shown — never a fabricated price. Market snapshot, cost profile, and
// diagnostics render observed/derived evidence; contribution is a placeholder
// until readiness is Complete.
export function ProductDetail() {
  const t = useT();
  const { locale } = useLocale();
  const variantId = useRouterState({
    select: (s) => (s.location.search as { variantId?: string }).variantId,
  });

  const productQuery = useCatalogProduct(variantId);
  const product = productQuery.data;
  const readinessQuery = useMarginReadiness(variantId);
  const profilesQuery = useCostProfiles(variantId);

  // READ-ONLY listing/image diagnostics (LST-001), derived from captured catalog
  // data. This is the REAL diagnostics seam — NOT observation capture provenance
  // (sourceType/parserVersion are how an offer was OBSERVED, never a listing/image
  // verdict). Its absence never hides the canonical product.
  const diagnosticsQuery = useProductDiagnostics(variantId);
  const diagnostics = diagnosticsQuery.data?.items ?? [];

  const readiness = readinessQuery.data;
  const offer = product ? primaryOffer(product.marketOffers) : undefined;
  const profileByComponent = new Map<CostComponent, CostProfileVersion>();
  for (const p of profilesQuery.data?.items ?? []) profileByComponent.set(p.component, p);

  return (
    <div className="screen">
      <div className="toolbar">
        <AppLink to="/products" className="link">
          {t("product.back")}
        </AppLink>
        {/* Native product ID is a technical identifier: raw + LTR-isolated. */}
        {product ? <LtrToken text={String(product.nativeProductId)} /> : null}
      </div>

      <ViewState
        pending={productQuery.isPending}
        error={productQuery.isError}
        onRetry={() => void productQuery.refetch()}
      >
        {!variantId || !product ? (
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
                {product.ownedOffer.present && product.ownedOffer.price ? (
                  <div className="kv">
                    <div className="kv__row">
                      <span>{t("product.ownedOffer.price")}</span>
                      <LtrToken text={product.ownedOffer.price.text} />
                    </div>
                    {typeof product.ownedOffer.sellerStock === "number" ? (
                      <div className="kv__row">
                        <span>{t("product.ownedOffer.sellerStock")}</span>
                        <span>
                          {t("product.stock.signal", {
                            count: formatCount(product.ownedOffer.sellerStock, locale),
                          })}
                        </span>
                      </div>
                    ) : null}
                    {typeof product.ownedOffer.warehouseStock === "number" ? (
                      <div className="kv__row">
                        <span>{t("product.ownedOffer.warehouseStock")}</span>
                        <span>
                          {t("product.stock.signal", {
                            count: formatCount(product.ownedOffer.warehouseStock, locale),
                          })}
                        </span>
                      </div>
                    ) : null}
                  </div>
                ) : (
                  <div className="kv">
                    <div className="kv__row">
                      <CapabilityBadge state={product.ownedOffer.capability} />
                    </div>
                    <p className="muted">{t(ownedReasonKey(product.ownedOffer))}</p>
                  </div>
                )}
              </Section>

              <Section titleKey="product.section.mapping">
                <div className="kv">
                  <div className="kv__row">
                    <span>{t("product.mapping.state")}</span>
                    <MappingBadge state={product.mappingState} />
                  </div>
                  <div className="kv__row">
                    <span>{t("product.mapping.watch")}</span>
                    <span className="muted">
                      {t(product.watched ? "mapping.watched" : "mapping.unwatched")}
                    </span>
                  </div>
                </div>
              </Section>

              <Section titleKey="product.section.snapshot">
                {offer ? (
                  <div className="kv">
                    <div className="kv__row">
                      <span>{t("product.marketPrice")}</span>
                      <span className="inline-badges">
                        <LtrToken text={offer.offerIdentity} />
                        <LtrToken text={offer.price.text} />
                      </span>
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
                {readinessQuery.isError ? (
                  <SectionError
                    titleKey="product.readiness.error.title"
                    bodyKey="product.readiness.error.body"
                    testId="product-contribution-error"
                    onRetry={() => void readinessQuery.refetch()}
                  />
                ) : readiness?.state === "complete" ? (
                  <p className="muted">{t("common.notAvailable")}</p>
                ) : (
                  <p className="muted">{t("product.contribution.placeholder")}</p>
                )}
              </Section>

              <Section titleKey="product.section.readiness">
                {readinessQuery.isError ? (
                  <SectionError
                    titleKey="product.readiness.error.title"
                    bodyKey="product.readiness.error.body"
                    testId="product-readiness-error"
                    onRetry={() => void readinessQuery.refetch()}
                  />
                ) : readiness ? (
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
              {profilesQuery.isError ? (
                <SectionError
                  titleKey="product.cost.error.title"
                  bodyKey="product.cost.error.body"
                  testId="product-cost-error"
                  onRetry={() => void profilesQuery.refetch()}
                />
              ) : (
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
              )}
            </Section>

            <Section
              titleKey="product.section.diagnostics"
              actions={
                <AppLink to="/diagnostics" search={{ variantId }} className="link">
                  {t("product.diagnostics.viewAll")}
                </AppLink>
              }
            >
              {diagnosticsQuery.isError ? (
                <SectionError
                  titleKey="product.diagnostics.error.title"
                  bodyKey="product.diagnostics.error.body"
                  testId="product-diagnostics-error"
                  onRetry={() => void diagnosticsQuery.refetch()}
                />
              ) : diagnostics.length > 0 ? (
                <dl className="kv">
                  {diagnostics.map((d) => (
                    <div key={`${d.field}.${d.ruleId}`} className="kv__row">
                      <span>{t(DIAGNOSTIC_FIELD_LABEL[d.field])}</span>
                      <span className="inline-badges">
                        <DiagnosticResultBadge state={d.result} />
                        {/* Rule id/version are technical identifiers: raw + LTR. */}
                        <LtrToken text={d.ruleId} />
                      </span>
                    </div>
                  ))}
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
