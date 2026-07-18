import type { MessageKey } from "@market-ops/locale";
import { useT } from "../../app/i18n";
import type { StatementKind } from "../types";

// The seven CHAT-004 statement kinds, each a visually-distinct section so a model
// inference is never presented as an observed market fact. Mirrors the EvidencePanel
// pattern (colored header dot + text label; inference in an accent panel with an
// explicit note). The kind→label map is DATA; labels resolve through the catalog.

const KIND_LABEL: Record<StatementKind, MessageKey> = {
  observed: "chat.statement.observed",
  dk: "chat.statement.dk",
  config: "chat.statement.config",
  calculation: "chat.statement.calculation",
  inference: "chat.statement.inference",
  missing: "chat.statement.missing",
  recommendation: "chat.statement.recommendation",
};

export function StatementSection({
  kind,
  lines,
}: {
  kind: StatementKind;
  lines: readonly string[];
}) {
  const t = useT();
  const label =
    kind === "dk"
      ? t("chat.statement.dk", { marketplace: t("marketplace.name") })
      : t(KIND_LABEL[kind]);
  return (
    <section className="statement" data-kind={kind} data-testid={`statement-${kind}`}>
      <div className="statement__head">
        <span className="statement__dot" aria-hidden />
        <span className="statement__title">{label}</span>
      </div>
      {kind === "inference" ? (
        <p className="statement__note">{t("chat.statement.inferenceNote")}</p>
      ) : null}
      <ul className="statement__lines">
        {lines.map((line, i) => (
          // Positional index key: a completed message's lines are static and never
          // reorder (the envelope is immutable once the `final` frame lands).
          // biome-ignore lint/suspicious/noArrayIndexKey: static positional response data
          <li key={`${i}:${line}`} className="statement__line">
            {line}
          </li>
        ))}
      </ul>
    </section>
  );
}
