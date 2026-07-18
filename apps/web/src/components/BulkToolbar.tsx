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
// button is bound to the EXACT previewed selection-set version. ANY change to the
// set or its filters mints a new version; the previewed version then diverges and
// the control renders DISABLED behind an invalidation banner that requires a fresh
// preview. Free text, Enter, and keyboard shortcuts CANNOT confirm — the only path
// to a bulk approval is this button, and it carries the bound version to the
// server, which re-verifies it (a stale bound version is rejected).

export interface BulkCounts {
  readonly executable: number;
  readonly warning: number;
  readonly blocked: number;
}

export function BulkToolbar({
  lineage,
  version,
  previewedVersion,
  counts,
  aggregateImpact,
  maxMovement,
  exclusions,
  confirmPending = false,
  onPreview,
  onApprove,
}: {
  lineage: string;
  version: number;
  previewedVersion: number | null;
  counts: BulkCounts;
  aggregateImpact: ReactNode;
  maxMovement: ReactNode;
  exclusions: ReactNode;
  confirmPending?: boolean;
  onPreview: () => void;
  onApprove: () => void;
}) {
  const t = useT();
  const { locale } = useLocale();

  const previewValid = previewedVersion !== null && previewedVersion === version;
  const stale = previewedVersion !== null && previewedVersion !== version;
  const canApprove = previewValid && counts.executable > 0 && !confirmPending;

  return (
    <section
      className="panel bulk-toolbar"
      data-testid="bulk-toolbar"
      data-set-version={version}
      data-previewed-version={previewedVersion ?? ""}
      data-preview-valid={previewValid ? "true" : "false"}
    >
      <div className="panel__head">
        <h2 className="panel__title">{t("bulk.preview.title")}</h2>
        <span className="muted" data-testid="selection-set" data-version={version}>
          {t("bulk.selectionSet")} <LtrToken text={`${lineage}·v${version}`} />
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
          onClick={onPreview}
        >
          {previewedVersion === null ? t("bulk.action.preview") : t("bulk.action.rePreview")}
        </button>
        <button
          type="button"
          className="btn btn--primary"
          data-testid="bulk-approve"
          disabled={!canApprove}
          onClick={onApprove}
        >
          {t("bulk.action.approve", { count: formatCount(counts.executable, locale) })}
        </button>
      </div>

      <p className="approval-card__footnote muted" data-testid="bulk-footnote">
        {t("bulk.footnote")}
      </p>
    </section>
  );
}
