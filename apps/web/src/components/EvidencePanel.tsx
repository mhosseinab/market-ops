import type { MessageKey } from "@market-ops/locale";
import type { ReactNode } from "react";
import { useT } from "../app/i18n";

// EvidencePanel (component inventory): the four-way evidence separation required
// on the event-detail surface (design screen 4). Each kind carries a colored
// header dot AND a text label; the `inference` kind renders in an accent panel and
// is EXPLICITLY labeled "model inference, not observed fact" — a model claim is
// never presented as an observed market fact (§8 free-text containment spirit).

export type EvidenceKind = "observed" | "dk" | "config" | "inference";

const KIND_LABEL: Record<EvidenceKind, MessageKey> = {
  observed: "event.evidence.observed",
  dk: "event.evidence.dk",
  config: "event.evidence.config",
  inference: "event.evidence.inference",
};

export function EvidencePanel({ kind, children }: { kind: EvidenceKind; children: ReactNode }) {
  const t = useT();
  const label =
    kind === "dk"
      ? t("event.evidence.dk", { marketplace: t("marketplace.name") })
      : t(KIND_LABEL[kind]);
  return (
    <div className="evidence-panel" data-kind={kind}>
      <div className="evidence-panel__head">
        <span className="evidence-panel__dot" aria-hidden />
        <span className="evidence-panel__title">{label}</span>
      </div>
      {kind === "inference" ? (
        <p className="evidence-panel__note">{t("event.evidence.inferenceNote")}</p>
      ) : null}
      <div className="evidence-panel__body">{children}</div>
    </div>
  );
}
