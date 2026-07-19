import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";
import type { ConnectorHealth } from "../data/connectorHealth";

// Issue #18 — the TopBar connection pill. It renders an ALREADY-DERIVED connector
// health (see data/connectorHealth.ts); it never re-derives from raw status, so
// it can never disagree with the shared rule. The tone/label table below is a
// DATA map (no locale/direction branch); labels resolve through the catalog
// (zero string literals). FAIL CLOSED: only `supported` carries the positive
// tone — every other health is neutral/warning/negative (PRD §4.6, ACC-001).

type Tone = "tone-muted" | "tone-risk" | "tone-info" | "tone-warn" | "tone-pos";

const PRESENTATION: Record<ConnectorHealth, { tone: Tone; labelKey: MessageKey }> = {
  unknown: { tone: "tone-muted", labelKey: "topbar.connection.unknown" },
  disconnected: { tone: "tone-risk", labelKey: "topbar.connection.disconnected" },
  probing: { tone: "tone-info", labelKey: "topbar.connection.probing" },
  degraded: { tone: "tone-warn", labelKey: "topbar.connection.degraded" },
  supported: { tone: "tone-pos", labelKey: "topbar.connection.healthy" },
};

export function ConnectorHealthPill({ health }: { health: ConnectorHealth }) {
  const t = useT();
  const { tone, labelKey } = PRESENTATION[health];
  const label = t(labelKey);
  return (
    <span
      className={`connection-pill ${tone}`}
      role="status"
      data-health={health}
      aria-label={t("topbar.connection.aria", { status: label })}
    >
      <span className="badge__dot" aria-hidden />
      {label}
    </span>
  );
}
