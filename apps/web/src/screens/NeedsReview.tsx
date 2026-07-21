import { useState } from "react";
import { useLocale, useT } from "../app/i18n";
import { type Column, DataTable } from "../components/DataTable";
import { LtrToken } from "../components/LtrToken";
import { MutationError } from "../components/MutationError";
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
  // Note drafts are keyed by identity — the note is audit evidence that belongs
  // to ONE candidate. A global draft would let A's text be submitted against B
  // (cross-identity attribution, #83). The visible note is strictly the selected
  // identity's own draft; an unselected/absent identity yields empty (fail-closed),
  // never another candidate's text.
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const note = selectedId ? (drafts[selectedId] ?? "") : "";
  function setNote(value: string) {
    if (!selectedId) return;
    setDrafts((prev) => ({ ...prev, [selectedId]: value }));
  }

  const items = query.data?.items ?? [];
  const selected = items.find((i) => i.identityId === selectedId);

  // At most one decision runs at a time; surface whichever failed. The note is a
  // free-text field, not authority — but it IS the reviewer's input, so it is
  // preserved on failure (cleared only once the decision is recorded).
  const failed = confirm.isError ? confirm : reject.isError ? reject : defer.isError ? defer : null;
  // A decision in flight locks EVERY control: no concurrent confirm/reject/defer
  // on any candidate while one is pending.
  const anyPending = confirm.isPending || reject.isPending || defer.isPending;

  // Audit-attribution never-cut (#83): a decision always acts on the SELECTED
  // candidate — the one whose evidence panel (and note) is visible. The row
  // controls are inert unless their row is the selected action target, so the
  // payload identity is structurally the visible evidence, never a mismatched
  // candidate, and the note travels only with the identity it was typed against.
  function decide(fn: typeof confirm) {
    if (!selected) return;
    fn.mutate(
      { identityId: selected.identityId, ...(note.trim() ? { note: note.trim() } : {}) },
      { onSuccess: () => setNote("") },
    );
  }

  function dismissDecisionError() {
    confirm.reset();
    reject.reset();
    defer.reset();
  }

  const columns: readonly Column<NeedsReviewItem>[] = [
    { id: "sku", header: "needsReview.col.sku", render: (r) => <LtrToken text={r.supplierCode} /> },
    { id: "variant", header: "needsReview.col.variant", render: (r) => r.variantTitle },
    { id: "product", header: "needsReview.col.product", render: (r) => r.productTitle },
    {
      id: "decision",
      header: "needsReview.col.decision",
      render: (r) => {
        // Only the selected candidate is a valid action target; while any
        // decision is pending, every control is locked.
        const locked = anyPending || r.identityId !== selectedId;
        return (
          <div className="row-actions">
            <button
              type="button"
              className="btn btn--primary btn--sm"
              disabled={locked}
              onClick={() => decide(confirm)}
            >
              {t("needsReview.confirm")}
            </button>
            <button
              type="button"
              className="btn btn--danger btn--sm"
              disabled={locked}
              onClick={() => decide(reject)}
            >
              {t("needsReview.reject")}
            </button>
            <button
              type="button"
              className="btn btn--secondary btn--sm"
              disabled={locked}
              onClick={() => decide(defer)}
            >
              {t("needsReview.defer")}
            </button>
          </div>
        );
      },
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
        {/* A quarantined identity only moves on a structured decision; a failed
            decision is surfaced with the note preserved. The row's own
            confirm/reject/defer controls remain the explicit re-decision path. */}
        {failed ? (
          <MutationError
            testId="decision-error"
            error={failed.error}
            guidanceKey="needsReview.decision.error"
            onDismiss={dismissDecisionError}
          />
        ) : null}
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
                    {/* Native IDs are technical identifiers, not quantities:
                        render the raw value LTR-isolated (no grouping, no digit
                        conversion) so they stay cross-referenceable against DK. */}
                    <LtrToken text={String(selected.nativeVariantId)} />
                  </dd>
                  <dt>{t("needsReview.evidence.nativeProduct")}</dt>
                  <dd>
                    <LtrToken text={String(selected.nativeProductId)} />
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
