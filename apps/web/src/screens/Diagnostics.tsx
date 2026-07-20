import type { MessageKey } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useLocale, useT } from "../app/i18n";
import { AppLink } from "../components/AppLink";
import { DiagnosticResultBadge } from "../components/badges";
import { LtrToken } from "../components/LtrToken";
import { ViewState } from "../components/ViewState";
import { formatCount, formatInstant } from "../data/format";
import { useProductDiagnostics } from "../data/hooks";
import type {
  ListingDiagnostic,
  ListingDiagnosticEntity,
  ListingDiagnosticField,
  ListingObservedState,
} from "../data/types";

const FIELD_LABEL: Record<ListingDiagnosticField, MessageKey> = {
  title: "diagnostics.field.title",
  description: "diagnostics.field.description",
  image: "diagnostics.field.image",
};

const ENTITY_LABEL: Record<ListingDiagnosticEntity, MessageKey> = {
  product: "diagnostics.entity.product",
  variant: "diagnostics.entity.variant",
  listing: "diagnostics.entity.listing",
};

const OBSERVED_LABEL: Record<ListingObservedState, MessageKey> = {
  present: "diagnostics.observed.present",
  empty: "diagnostics.observed.empty",
  not_observed: "diagnostics.observed.notObserved",
};

// A stable key per diagnostic row (entity+field+rule uniquely identify one).
function rowKey(d: ListingDiagnostic): string {
  return `${d.entity}.${d.field}.${d.ruleId}.${d.ruleVersion}`;
}

// Diagnostics (design screen 13). READ-ONLY listing/image diagnostics (LST-001):
// every row NAMES the observed entity + field and the rule id/version it was
// evaluated against, shows a pass/warn result, the observed-value metadata
// (presence/length — never content), a stable evidence reference, and the capture
// time. There is NO generate/publish/auto-fix control anywhere on this screen —
// diagnostics report, they never act. The deep link carries a variantId; without
// it, a reassuring empty prompt is shown (never a fabricated report).
export function Diagnostics() {
  const t = useT();
  const { locale } = useLocale();
  const variantId = useRouterState({
    select: (s) => (s.location.search as { variantId?: string }).variantId,
  });

  const query = useProductDiagnostics(variantId);
  const items = query.data?.items ?? [];

  return (
    <div className="screen">
      <div className="toolbar">
        <AppLink to="/products" className="link">
          {t("product.back")}
        </AppLink>
      </div>

      <header className="screen__header">
        <h1 className="screen__title">{t("route.diagnostics.title")}</h1>
        <p className="muted">{t("route.diagnostics.sub")}</p>
        {/* LST-001: make the read-only posture explicit — no content is changed. */}
        <p className="muted">{t("diagnostics.readOnlyNote")}</p>
      </header>

      {!variantId ? (
        <div className="screen-empty">
          <p>{t("diagnostics.noVariant")}</p>
        </div>
      ) : (
        <ViewState
          pending={query.isPending}
          error={query.isError}
          isEmpty={items.length === 0}
          onRetry={() => void query.refetch()}
        >
          <ul className="diagnostics-list">
            {items.map((d) => (
              <li key={rowKey(d)} className="diagnostics-list__item" data-result={d.result}>
                <div className="diagnostics-list__head">
                  <span className="diagnostics-list__field">{t(FIELD_LABEL[d.field])}</span>
                  <DiagnosticResultBadge state={d.result} />
                </div>
                <dl className="kv">
                  <div className="kv__row">
                    <span>{t("diagnostics.entityLabel")}</span>
                    <span className="muted">{t(ENTITY_LABEL[d.entity])}</span>
                  </div>
                  <div className="kv__row">
                    <span>{t("diagnostics.rule")}</span>
                    {/* Rule id + version are technical identifiers: raw + LTR-isolated. */}
                    <span className="inline-badges">
                      <LtrToken text={d.ruleId} />
                      <LtrToken text={d.ruleVersion} />
                    </span>
                  </div>
                  <div className="kv__row">
                    <span>{t("diagnostics.observed.label")}</span>
                    <span className="inline-badges">
                      <span className="muted">{t(OBSERVED_LABEL[d.observed.state])}</span>
                      {typeof d.observed.characterLength === "number" ? (
                        <span className="muted">
                          {t("diagnostics.observed.length", {
                            count: formatCount(d.observed.characterLength, locale),
                          })}
                        </span>
                      ) : null}
                    </span>
                  </div>
                  <div className="kv__row">
                    <span>{t("diagnostics.evidence")}</span>
                    <LtrToken text={d.evidenceRef} />
                  </div>
                  <div className="kv__row">
                    <span>{t("diagnostics.capturedAt")}</span>
                    <span className="muted">{formatInstant(d.capturedAt, locale)}</span>
                  </div>
                </dl>
              </li>
            ))}
          </ul>
        </ViewState>
      )}
    </div>
  );
}
