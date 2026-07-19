import type { MessageKey } from "@market-ops/locale";
import { normalizeDigits, parseNumericInput } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { DispositionBadge } from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { Section, StatCard } from "../components/primitives";
import { formatCount } from "../data/format";
import { useCostImportCommit, useCostImportPreview, useSingleCostEntry } from "../data/hooks";
import type { CostComponent, CostImportRow } from "../data/types";

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

const REASON_KEY: Record<string, MessageKey> = {
  sku_not_found: "costReason.sku_not_found",
  ambiguous_sku: "costReason.ambiguous_sku",
  negative_amount: "costReason.negative_amount",
  invalid_amount: "costReason.invalid_amount",
  duplicate_in_file: "costReason.duplicate_in_file",
};

function ReasonCell({ reason }: { reason: string }) {
  const t = useT();
  if (reason === "") return null;
  const key = REASON_KEY[reason];
  if (key) return <span>{t(key)}</span>;
  return <LtrToken text={reason} />;
}

// Value cell as a named component so the table `render` arrow returns a single
// element (avoids the copy-lint / biome disagreement over inline ternaries). The
// parsed amount renders as Money; a non-accept row shows its normalized token.
function ValueCell({ row }: { row: CostImportRow }) {
  if (row.amount) return <MoneyView amount={row.amount} />;
  return <LtrToken text={row.normalizedValue} />;
}

// Cost import (design screen 5, CST-001/002). A CSV is PREVIEWED before it can
// commit: every row gets a disposition + a stated reason for any non-accept, and
// a duplicate (SKU, component) conflict BLOCKS commit until resolved. Commit acts
// on the previewed batch id only. The manual single-value form records one
// component version (CST-002), digits normalized at the input boundary.
export function CostImport() {
  const t = useT();
  const { locale } = useLocale();
  const initialVariant = useRouterState({
    select: (s) => (s.location.search as { variantId?: string }).variantId ?? "",
  });

  const [csv, setCsv] = useState("");
  const [filename, setFilename] = useState<string | undefined>(undefined);
  const preview = useCostImportPreview();
  const commit = useCostImportCommit();

  const batch = preview.data;
  const hasDuplicates = (batch?.counts.duplicate ?? 0) > 0;

  // Any change to the CSV source invalidates a prior preview (and any commit
  // result bound to it): the previewed batch id no longer matches what would be
  // committed, so it must not stay committable (CST-001; §4.6 — a stale card is
  // never left clickable). Resetting the mutations removes the preview section
  // and its commit control until a fresh preview is requested.
  function changeSource(text: string, name?: string) {
    setCsv(text);
    setFilename(name);
    preview.reset();
    commit.reset();
  }

  async function onFile(file: File) {
    const text = await file.text();
    changeSource(text, file.name);
  }

  const columns: readonly Column<CostImportRow>[] = [
    { id: "sku", header: "cost.col.sku", render: (r) => <LtrToken text={r.sku} /> },
    {
      id: "value",
      header: "cost.col.value",
      render: (r) => <ValueCell row={r} />,
    },
    {
      id: "status",
      header: "cost.col.status",
      render: (r) => <DispositionBadge state={r.disposition} />,
    },
    { id: "note", header: "cost.col.note", render: (r) => <ReasonCell reason={r.reason} /> },
  ];

  return (
    <div className="screen">
      <Section titleKey="cost.dropzone.title">
        <p className="muted">{t("cost.dropzone.hint")}</p>
        <label className="field">
          <span className="field__label">{t("cost.file.choose")}</span>
          <input
            type="file"
            accept=".csv,text/csv"
            data-testid="cost-file"
            onChange={(e) => {
              const file = e.target.files?.[0];
              if (file) void onFile(file);
            }}
          />
        </label>
        <textarea
          className="field__input ltr"
          data-testid="cost-csv"
          value={csv}
          onChange={(e) => changeSource(e.target.value)}
          rows={4}
          aria-label={t("cost.dropzone.title")}
        />
        <button
          type="button"
          className="btn btn--primary"
          data-testid="cost-preview"
          disabled={preview.isPending || csv.trim() === ""}
          onClick={() => preview.mutate({ csv, ...(filename ? { filename } : {}) })}
        >
          {t("cost.preview.title")}
        </button>
      </Section>

      {batch ? (
        <Section titleKey="cost.preview.title">
          <div className="stat-row">
            <StatCard
              value={formatCount(batch.counts.accept, locale)}
              labelKey="cost.count.accept"
              accent="pos"
            />
            <StatCard
              value={formatCount(batch.counts.reject, locale)}
              labelKey="cost.count.reject"
              accent="risk"
            />
            <StatCard
              value={formatCount(batch.counts.duplicate, locale)}
              labelKey="cost.count.duplicate"
              accent="warn"
            />
          </div>

          <DataTable columns={columns} rows={batch.rows} rowKey={(r) => String(r.rowNumber)} />

          {hasDuplicates ? (
            <p className="blocker-note" role="alert">
              {t("cost.duplicateBlock", { count: formatCount(batch.counts.duplicate, locale) })}
            </p>
          ) : null}

          {commit.data ? (
            <p className="success-note">
              {t("cost.committed", { count: formatCount(commit.data.committedRows, locale) })}
            </p>
          ) : (
            <button
              type="button"
              className="btn btn--primary"
              data-testid="cost-commit"
              disabled={hasDuplicates || commit.isPending || batch.counts.accept === 0}
              onClick={() => commit.mutate(batch.batchId)}
            >
              {t("cost.confirm", { count: formatCount(batch.counts.accept, locale) })}
            </button>
          )}
        </Section>
      ) : null}

      <SingleCostForm initialVariant={initialVariant} />
    </div>
  );
}

function SingleCostForm({ initialVariant }: { initialVariant: string }) {
  const t = useT();
  const { locale } = useLocale();
  const entry = useSingleCostEntry();
  const [variantId, setVariantId] = useState(initialVariant);
  const [component, setComponent] = useState<CostComponent>("cogs");
  const [rawValue, setRawValue] = useState("");
  const [rawUnit, setRawUnit] = useState("");
  const [invalid, setInvalid] = useState(false);

  function submit() {
    // Normalize Persian/Latin digits at the input boundary (LOC-007) and reject
    // a non-numeric value before it reaches the core.
    if (parseNumericInput(rawValue) === null) {
      setInvalid(true);
      return;
    }
    setInvalid(false);
    entry.mutate({
      variantId,
      component,
      rawValue: normalizeDigits(rawValue),
      ...(rawUnit.trim() ? { rawUnit } : {}),
    });
  }

  return (
    <Section titleKey="cost.single.title">
      <label className="field">
        <span className="field__label">{t("cost.single.variant")}</span>
        <input
          className="field__input ltr"
          value={variantId}
          onChange={(e) => setVariantId(e.target.value)}
        />
      </label>
      <label className="field">
        <span className="field__label">{t("cost.single.component")}</span>
        <select
          className="field__input"
          value={component}
          onChange={(e) => setComponent(e.target.value as CostComponent)}
        >
          {COST_COMPONENTS.map((c) => (
            <option key={c} value={c}>
              {t(COMPONENT_LABEL[c])}
            </option>
          ))}
        </select>
      </label>
      <label className="field">
        <span className="field__label">{t("cost.single.value")}</span>
        <input
          className="field__input"
          data-testid="single-value"
          value={rawValue}
          onChange={(e) => setRawValue(e.target.value)}
        />
      </label>
      <label className="field">
        <span className="field__label">{t("cost.single.unit")}</span>
        <input
          className="field__input"
          value={rawUnit}
          onChange={(e) => setRawUnit(e.target.value)}
        />
      </label>
      {invalid ? (
        <p className="blocker-note" role="alert">
          {t("cost.invalidNumber")}
        </p>
      ) : null}
      {entry.data ? (
        <p className="success-note">
          {t("cost.single.success", {
            version: formatCount(entry.data.version, locale),
            component: t(COMPONENT_LABEL[entry.data.component]),
          })}
        </p>
      ) : null}
      <button
        type="button"
        className="btn btn--primary"
        data-testid="single-submit"
        disabled={entry.isPending || variantId.trim() === "" || rawValue.trim() === ""}
        onClick={submit}
      >
        {t("cost.single.submit")}
      </button>
    </Section>
  );
}
