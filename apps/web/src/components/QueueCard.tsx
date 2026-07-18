import type { MessageKey } from "@market-ops/locale";
import type { ReactNode } from "react";
import { useT } from "../app/i18n";

// QueueCard (component inventory): one Operations queue tile — a left-accent
// colored card with a title, a count (or an explicit unavailable node when the
// count is not surfaced, never blank-as-zero), a description, an optional runbook
// link, and an "open queue" action. Copy resolves through the catalog; the accent
// carries meaning but the title/description always accompany it.

export type QueueAccent = "risk" | "warn" | "conflict" | "info" | "ink2";

export function QueueCard({
  titleKey,
  descKey,
  accent,
  count,
  runbook,
  open,
}: {
  titleKey: MessageKey;
  descKey: MessageKey;
  accent: QueueAccent;
  count: ReactNode;
  runbook?: ReactNode;
  open?: ReactNode;
}) {
  const t = useT();
  return (
    <section className="queue-card" data-accent={accent} data-testid="queue-card">
      <div className="queue-card__head">
        <h3 className="queue-card__title">{t(titleKey)}</h3>
        <span className="queue-card__count">{count}</span>
      </div>
      <p className="queue-card__desc muted">{t(descKey)}</p>
      <div className="queue-card__actions">
        {open ?? null}
        {runbook ?? null}
      </div>
    </section>
  );
}
