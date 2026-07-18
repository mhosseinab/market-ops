import { useLocale, useT } from "../../app/i18n";
import { QualityBadge } from "../../components/badges";
import { LtrToken } from "../../components/LtrToken";
import { formatInstant } from "../../data/format";
import type { EvidenceRef } from "../types";

// Evidence references + capture times accompanying operational claims (CHAT-005).
// Missing evidence fails closed: no evidence renders the explicit missing state,
// never a bare claim. Each ref's id is an LTR-isolated technical identifier.
export function EvidenceRefs({ evidence }: { evidence: readonly EvidenceRef[] }) {
  const t = useT();
  const { locale } = useLocale();
  return (
    <div className="chat-evidence" data-testid="chat-evidence">
      <p className="chat-evidence__title">{t("chat.evidence.title")}</p>
      {evidence.length === 0 ? (
        <p className="chat-evidence__missing" data-testid="chat-evidence-missing">
          {t("chat.evidence.missing")}
        </p>
      ) : (
        <ul className="chat-evidence__list">
          {evidence.map((e) => (
            <li key={e.ref} className="chat-evidence__item">
              <span className="chat-evidence__ref">
                {t("chat.evidence.ref")} <LtrToken text={e.ref} />
              </span>
              {e.quality ? <QualityBadge state={e.quality} /> : null}
              {e.capturedAt ? (
                <span className="chat-evidence__age">
                  {t("chat.evidence.capturedAt", { time: formatInstant(e.capturedAt, locale) })}
                </span>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
