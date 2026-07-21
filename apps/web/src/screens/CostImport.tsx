import type { MessageKey } from "@market-ops/locale";
import { normalizeDigits, parseNumericInput } from "@market-ops/locale";
import { useRouterState } from "@tanstack/react-router";
import { useRef, useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { DispositionBadge } from "../components/badges";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { MoneyView } from "../components/MoneyView";
import { MutationError } from "../components/MutationError";
import { Section, StatCard } from "../components/primitives";
import { formatCount } from "../data/format";
import { useCostImportCommit, useCostImportPreview, useSingleCostEntry } from "../data/hooks";
import type {
  CostComponent,
  CostImportPreview as CostImportPreviewData,
  CostImportRow,
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

// The detected header→component mapping is part of the financial meaning the
// seller confirms (CST-001): an unexpected assignment commits valid-looking
// values into the WRONG cost-profile component and corrupts contribution
// economics. So the mapping must be shown AND be internally consistent before
// commit; anything missing or ambiguous fails closed (money-adjacent guard).
type MappingCheck =
  | { readonly resolved: true }
  | { readonly resolved: false; readonly reasonKey: MessageKey };

function resolveMapping(batch: CostImportPreviewData): MappingCheck {
  const detected = batch.detected;
  // Missing evidence: the server echoed no mapping (or an empty one). Never let
  // a value commit into an unshown component.
  if (!detected || detected.skuColumn.trim() === "" || detected.componentColumns.length === 0) {
    return { resolved: false, reasonKey: "cost.mapping.missing" };
  }
  // Ambiguous evidence: a row names a component the detected mapping does not
  // cover — the shown mapping and the committed rows would diverge.
  const mapped = new Set(detected.componentColumns.map((c) => c.component));
  if (batch.rows.some((r) => !mapped.has(r.component))) {
    return { resolved: false, reasonKey: "cost.mapping.ambiguous" };
  }
  return { resolved: true };
}

function ComponentCell({ component }: { component: CostComponent }) {
  const t = useT();
  return <span>{t(COMPONENT_LABEL[component])}</span>;
}

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
  // Monotonic source-selection generation. Every new source (a file pick or a
  // textarea edit) advances it; an async file read binds to the generation live
  // at pick time so a superseded read can be dropped on resolve (see onFile).
  const sourceGeneration = useRef(0);
  const preview = useCostImportPreview();
  const commit = useCostImportCommit();

  const batch = preview.data;
  const hasDuplicates = (batch?.counts.duplicate ?? 0) > 0;
  // Fail closed: an unshown or inconsistent mapping blocks commit (CST-001; §4.6
  // money-adjacent). Evaluated only when a batch exists.
  const mapping = batch ? resolveMapping(batch) : null;

  // Any change to the CSV source invalidates a prior preview (and any commit
  // result bound to it): the previewed batch id no longer matches what would be
  // committed, so it must not stay committable (CST-001; §4.6 — a stale card is
  // never left clickable). Resetting the mutations removes the preview section
  // and its commit control until a fresh preview is requested.
  function changeSource(text: string, name?: string) {
    // A new authoritative source: advance the generation so any file read still
    // in flight (or a slower read from an earlier pick) is superseded and will be
    // discarded when it resolves.
    sourceGeneration.current += 1;
    setCsv(text);
    setFilename(name);
    preview.reset();
    commit.reset();
  }

  async function onFile(file: File) {
    // Bind this read to the generation live at pick time. If a newer selection
    // (another file pick or a textarea edit) supersedes it before file.text()
    // resolves, the read is DISCARDED — an older, slower read must never
    // overwrite the newer parse (fail closed; import-boundary correctness, #79).
    sourceGeneration.current += 1;
    const generation = sourceGeneration.current;
    const text = await file.text();
    if (generation !== sourceGeneration.current) return;
    changeSource(text, file.name);
  }

  const columns: readonly Column<CostImportRow>[] = [
    { id: "sku", header: "cost.col.sku", render: (r) => <LtrToken text={r.sku} /> },
    {
      id: "component",
      header: "cost.col.component",
      render: (r) => <ComponentCell component={r.component} />,
    },
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
          onClick={() => {
            // A new preview always begins with a fresh confirm control: reset any
            // prior commit result so a completed import (bound to an older batch)
            // never lingers over a freshly previewed batch (issue #79, acceptance
            // 4). This subsumes the source-change reset in changeSource.
            commit.reset();
            preview.mutate({ csv, ...(filename ? { filename } : {}) });
          }}
        >
          {t("cost.preview.title")}
        </button>
        {/* Preview mutates no state (it re-evaluates the CSV against current
            catalog/cost state), so a failed preview offers a direct retry that
            re-runs on the still-present file. */}
        {preview.isError ? (
          <MutationError
            testId="preview-error"
            error={preview.error}
            guidanceKey="cost.preview.error"
            onDismiss={() => preview.reset()}
            onRetry={() => {
              commit.reset();
              preview.mutate({ csv, ...(filename ? { filename } : {}) });
            }}
            retryPending={preview.isPending}
          />
        ) : null}
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

          {/* Detected header→component mapping (CST-001): the seller confirms
              WHICH column feeds WHICH cost component before any value commits.
              Header tokens are technical identifiers → LTR-isolated; component
              identities render through the canonical glossary label. */}
          {batch.detected ? (
            <div className="cost-mapping" data-testid="cost-mapping">
              <h3 className="cost-mapping__title">{t("cost.mapping.title")}</h3>
              <p className="muted">{t("cost.mapping.desc")}</p>
              <dl className="cost-mapping__list">
                <div className="cost-mapping__row">
                  <dt className="cost-mapping__term">{t("cost.mapping.skuColumn")}</dt>
                  <dd className="cost-mapping__def">
                    <LtrToken text={batch.detected.skuColumn} />
                  </dd>
                </div>
                {batch.detected.componentColumns.map((m) => (
                  <div className="cost-mapping__row" key={m.header}>
                    <dt className="cost-mapping__term">
                      <LtrToken text={m.header} />
                    </dt>
                    <dd className="cost-mapping__def">{t(COMPONENT_LABEL[m.component])}</dd>
                  </div>
                ))}
              </dl>
            </div>
          ) : null}

          <DataTable columns={columns} rows={batch.rows} rowKey={(r) => String(r.rowNumber)} />

          {mapping && !mapping.resolved ? (
            <p className="blocker-note" role="alert" data-testid="cost-mapping-block">
              {t(mapping.reasonKey)}
            </p>
          ) : null}

          {hasDuplicates ? (
            <p className="blocker-note" role="alert">
              {t("cost.duplicateBlock", { count: formatCount(batch.counts.duplicate, locale) })}
            </p>
          ) : null}

          {commit.isError ? (
            // A commit outcome is AMBIGUOUS on failure (it may or may not have
            // applied): NO one-click retry (acceptance 3). Dismiss clears the
            // stale preview so the ONLY path to commit again is a fresh preview —
            // a re-fetch of current state (CST-001; §4.6 stale card never left
            // clickable). The commit control is hidden while this shows.
            <MutationError
              testId="commit-error"
              error={commit.error}
              guidanceKey="cost.commit.error"
              onDismiss={() => {
                commit.reset();
                preview.reset();
              }}
            />
          ) : commit.data ? (
            <p className="success-note">
              {t("cost.committed", { count: formatCount(commit.data.committedRows, locale) })}
            </p>
          ) : (
            <button
              type="button"
              className="btn btn--primary"
              data-testid="cost-commit"
              disabled={
                hasDuplicates ||
                commit.isPending ||
                batch.counts.accept === 0 ||
                mapping?.resolved !== true
              }
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
      {/* Recording a value APPENDS a version; a failed attempt keeps the entered
          value/unit (input preserved) and the Record control is the explicit
          re-submit — no one-click retry that could double-append on an ambiguous
          outcome. */}
      {entry.isError ? (
        <MutationError
          testId="single-error"
          error={entry.error}
          guidanceKey="cost.single.error"
          onDismiss={() => entry.reset()}
        />
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
