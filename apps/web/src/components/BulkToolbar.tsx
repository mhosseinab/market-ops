import type { ReactNode } from "react";
import { useLocale, useT } from "../app/i18n";
import { formatCount } from "../data/format";
import { LtrToken } from "./LtrToken";
import { StatCard } from "./primitives";

// BulkToolbar (component inventory): the bulk-approval control surface. It shows
// the executable / warning / blocked counts, the aggregate impact + max movement
// (rendered exactly as the screen supplies them — an explicit unavailable node
// when policy math is not surfaced, never a fabricated zero), and the SINGLE
// structured approve control.
//
// APR-001 at the SET level (mirrors the individual ApprovalCard): the approve
// button is bound to the EXACT SERVER-MINTED selection-set version (issue #90). The
// lineage and version displayed here come from the server's preview response — this
// component never derives, counts, or defaults them. ANY change to the set or its
// filters makes the previewed selection stale; the owning screen then passes
// stale=true / previewValid=false and the control renders DISABLED behind an
// invalidation banner that requires a fresh preview. Free text, Enter, and keyboard
// shortcuts CANNOT confirm — the only path to a bulk approval is this button, and it
// carries the server's bound version, which the server re-verifies (a stale bound
// version authorizes nothing).
//
// Before the first preview there IS no selection-set identity: lineage and version
// are null and the identity renders as an explicit unavailable node, never a
// fabricated "v1".

export interface BulkCounts {
  readonly executable: number;
  readonly warning: number;
  readonly blocked: number;
}

export function BulkToolbar({
  lineage,
  version,
  previewValid,
  stale,
  counts,
  eligibleCount,
  aggregateImpact,
  maxMovement,
  exclusions,
  confirmPending = false,
  previewPending = false,
  onPreview,
  onApprove,
}: {
  lineage: string | null;
  version: number | null;
  previewValid: boolean;
  stale: boolean;
  /** ADVISORY per-OFFER-ROW counts for the stat cards. Rows are not members. */
  counts: BulkCounts;
  /**
   * What this control may HONESTLY claim it will authorize (issue #87). Sibling
   * offers on one target share ONE recommendation and therefore one selection
   * member, so a label built from the executable ROW count promised more than was
   * ever POSTed — a false statement of consent scope on the approval control itself
   * (APR-001). The owning screen supplies the requested membership before a preview
   * and the SERVER-SEALED executable member count after one.
   */
  eligibleCount: number;
  aggregateImpact: ReactNode;
  maxMovement: ReactNode;
  exclusions: ReactNode;
  confirmPending?: boolean;
  previewPending?: boolean;
  onPreview: () => void;
  onApprove: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();

  const canApprove = previewValid && eligibleCount > 0 && !confirmPending && !previewPending;

  return (
    <section
      className="panel bulk-toolbar"
      data-testid="bulk-toolbar"
      data-set-version={version ?? ""}
      data-preview-valid={previewValid ? "true" : "false"}
    >
      <div className="panel__head">
        <h2 className="panel__title">{t("bulk.preview.title")}</h2>
        <span className="muted" data-testid="selection-set" data-version={version ?? ""}>
          {t("bulk.selectionSet")}{" "}
          {lineage !== null && version !== null ? (
            <LtrToken text={`${lineage}·v${version}`} />
          ) : (
            <span>{t("common.notAvailable")}</span>
          )}
        </span>
      </div>

      <div className="stat-row">
        <StatCard
          value={formatCount(counts.executable, locale)}
          labelKey="bulk.count.executable"
          accent="pos"
        />
        <StatCard
          value={formatCount(counts.warning, locale)}
          labelKey="bulk.count.warning"
          accent="warn"
        />
        <StatCard
          value={formatCount(counts.blocked, locale)}
          labelKey="bulk.count.blocked"
          accent="risk"
        />
      </div>

      <dl className="kv bulk-toolbar__aggregate">
        <div className="kv__row">
          <dt>{t("bulk.aggregateImpact")}</dt>
          <dd>{aggregateImpact}</dd>
        </div>
        <div className="kv__row">
          <dt>{t("bulk.maxMovement")}</dt>
          <dd>{maxMovement}</dd>
        </div>
        <div className="kv__row">
          <dt>{t("bulk.exclusions")}</dt>
          <dd>{exclusions}</dd>
        </div>
      </dl>

      {stale ? (
        <div className="banner banner--warn" role="alert" data-testid="bulk-invalidated">
          <div className="banner__body">
            <p className="banner__title">{t("bulk.invalidated.title")}</p>
            <p className="banner__text">{t("bulk.invalidated.body")}</p>
          </div>
        </div>
      ) : null}

      <div className="bulk-toolbar__controls">
        <button
          type="button"
          className="btn btn--secondary"
          data-testid="bulk-preview"
          disabled={previewPending}
          onClick={onPreview}
        >
          {lineage === null ? t("bulk.action.preview") : t("bulk.action.rePreview")}
        </button>
        <button
          type="button"
          className="btn btn--primary"
          data-testid="bulk-approve"
          disabled={!canApprove}
          onClick={onApprove}
        >
          {t("bulk.action.approve", { count: formatCount(eligibleCount, locale) })}
        </button>
      </div>

      <p className="approval-card__footnote muted" data-testid="bulk-footnote">
        {t("bulk.footnote")}
      </p>
    </section>
  );
}
