import { useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { Section } from "../components/primitives";
import { ViewState } from "../components/ViewState";
import { formatCount } from "../data/format";
import { useIdentityDecision, useNeedsReview } from "../data/hooks";
import type { NeedsReviewItem } from "../data/types";

// Identity Needs Review queue (journey 4): confirm / reject / defer each pending
// Market Product Identity candidate. Identity is quarantined — only a structured
// decision moves it; the free-text note is audit evidence and carries no
// authority. The evidence panel shows the native-id evidence a reviewer needs.
export function NeedsReview() {
  const t = useT();
  const { locale } = useLocale();
  const query = useNeedsReview();
  const confirm = useIdentityDecision("/identity/confirm");
  const reject = useIdentityDecision("/identity/reject");
  const defer = useIdentityDecision("/identity/defer");
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined);
  const [note, setNote] = useState("");

  const items = query.data?.items ?? [];
  const selected = items.find((i) => i.identityId === selectedId);

  function decide(fn: typeof confirm, identityId: string) {
    fn.mutate({ identityId, ...(note.trim() ? { note: note.trim() } : {}) });
    setNote("");
  }

  const columns: readonly Column<NeedsReviewItem>[] = [
    { id: "sku", header: "needsReview.col.sku", render: (r) => <LtrToken text={r.supplierCode} /> },
    { id: "variant", header: "needsReview.col.variant", render: (r) => r.variantTitle },
    { id: "product", header: "needsReview.col.product", render: (r) => r.productTitle },
    {
      id: "decision",
      header: "needsReview.col.decision",
      render: (r) => (
        <div className="row-actions">
          <button
            type="button"
            className="btn btn--primary btn--sm"
            disabled={confirm.isPending}
            onClick={() => decide(confirm, r.identityId)}
          >
            {t("needsReview.confirm")}
          </button>
          <button
            type="button"
            className="btn btn--danger btn--sm"
            disabled={reject.isPending}
            onClick={() => decide(reject, r.identityId)}
          >
            {t("needsReview.reject")}
          </button>
          <button
            type="button"
            className="btn btn--secondary btn--sm"
            disabled={defer.isPending}
            onClick={() => decide(defer, r.identityId)}
          >
            {t("needsReview.defer")}
          </button>
        </div>
      ),
    },
  ];

  return (
    <Section titleKey="needsReview.title">
      <ViewState
        pending={query.isPending}
        error={query.isError}
        isEmpty={items.length === 0}
        onRetry={() => void query.refetch()}
      >
        <div className="split">
          <div className="split__main">
            <DataTable
              columns={columns}
              rows={items}
              rowKey={(r) => r.identityId}
              onRowClick={(r) => setSelectedId(r.identityId)}
              selectedId={selectedId}
            />
          </div>
          <aside className="split__aside">
            <h3 className="panel__subtitle">{t("needsReview.evidence.title")}</h3>
            {selected ? (
              <>
                <dl className="kv">
                  <dt>{t("needsReview.evidence.nativeVariant")}</dt>
                  <dd>
                    <LtrToken text={formatCount(selected.nativeVariantId, locale)} />
                  </dd>
                  <dt>{t("needsReview.evidence.nativeProduct")}</dt>
                  <dd>
                    <LtrToken text={formatCount(selected.nativeProductId, locale)} />
                  </dd>
                  <dt>{t("needsReview.evidence.candidateSource")}</dt>
                  <dd>
                    <LtrToken text={selected.candidateSource} />
                  </dd>
                  <dt>{t("needsReview.evidence.version")}</dt>
                  <dd>{formatCount(selected.version, locale)}</dd>
                </dl>
                <label className="field">
                  <span className="field__label">{t("needsReview.note.label")}</span>
                  <textarea
                    className="field__input"
                    value={note}
                    onChange={(e) => setNote(e.target.value)}
                  />
                </label>
              </>
            ) : (
              <p className="muted">{t("needsReview.selectHint")}</p>
            )}
          </aside>
        </div>
      </ViewState>
    </Section>
  );
}
