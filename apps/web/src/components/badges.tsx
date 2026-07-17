import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";

// Badge/pill primitives (design/IA_AND_COMPONENTS.md component inventory). Every
// badge pairs a semantic TONE with a text LABEL — color never stands alone. The
// state→{tone,labelKey} tables below are DATA maps, not locale/direction
// branches; labels resolve through the catalog (zero string literals).

type Tone =
  | "tone-pos"
  | "tone-risk"
  | "tone-warn"
  | "tone-info"
  | "tone-accent"
  | "tone-conflict"
  | "tone-muted"
  | "tone-ink2";

function Badge({
  tone,
  label,
  shape = "dot",
}: {
  tone: Tone;
  label: string;
  shape?: "dot" | "square" | "none";
}) {
  return (
    <span className={`badge badge--pill ${tone}`}>
      {shape !== "none" && (
        <span className={shape === "square" ? "badge__square" : "badge__dot"} aria-hidden />
      )}
      {label}
    </span>
  );
}

// ── Observation quality (design glossary) ──────────────────────────────────
export type QualityState =
  | "verified"
  | "supported"
  | "unverified"
  | "conflicted"
  | "stale"
  | "unavailable";

const QUALITY: Record<QualityState, { tone: Tone; key: MessageKey }> = {
  verified: { tone: "tone-pos", key: "state.verified" },
  supported: { tone: "tone-info", key: "state.supported" },
  unverified: { tone: "tone-muted", key: "state.unverified" },
  conflicted: { tone: "tone-conflict", key: "state.conflicted" },
  stale: { tone: "tone-warn", key: "state.stale" },
  unavailable: { tone: "tone-muted", key: "state.unavailable" },
};

export function QualityBadge({ state }: { state: QualityState }) {
  const t = useT();
  const m = QUALITY[state];
  return <Badge tone={m.tone} label={t(m.key)} />;
}

// ── Margin readiness (distinct axis; square marker) ────────────────────────
export type ReadinessState = "complete" | "partial" | "stale" | "missing";

const READINESS: Record<ReadinessState, { tone: Tone; key: MessageKey }> = {
  complete: { tone: "tone-pos", key: "readiness.complete" },
  partial: { tone: "tone-warn", key: "readiness.partial" },
  stale: { tone: "tone-warn", key: "readiness.stale" },
  missing: { tone: "tone-risk", key: "readiness.missing" },
};

export function ReadinessBadge({ state }: { state: ReadinessState }) {
  const t = useT();
  const m = READINESS[state];
  return <Badge tone={m.tone} label={t(m.key)} shape="square" />;
}

// ── Execution / lifecycle status ───────────────────────────────────────────
export type StatusState =
  | "awaitingConfirmation"
  | "executing"
  | "accepted"
  | "rejected"
  | "pendingReconciliation"
  | "failed"
  | "expired"
  | "blocked"
  | "simulation";

const STATUS: Record<StatusState, { tone: Tone; key: MessageKey }> = {
  awaitingConfirmation: { tone: "tone-ink2", key: "state.awaitingConfirmation" },
  executing: { tone: "tone-info", key: "state.executing" },
  accepted: { tone: "tone-pos", key: "state.accepted" },
  rejected: { tone: "tone-risk", key: "state.rejected" },
  pendingReconciliation: { tone: "tone-warn", key: "state.pendingReconciliation" },
  failed: { tone: "tone-risk", key: "state.failed" },
  expired: { tone: "tone-ink2", key: "state.expired" },
  blocked: { tone: "tone-risk", key: "state.blocked" },
  simulation: { tone: "tone-conflict", key: "state.simulation" },
};

export function StatusBadge({ state }: { state: StatusState }) {
  const t = useT();
  const m = STATUS[state];
  // "Accepted by {marketplace}" carries the parameterized marketplace name.
  const label =
    state === "accepted" ? t("state.accepted", { marketplace: t("marketplace.name") }) : t(m.key);
  return <Badge tone={m.tone} label={label} />;
}

// ── Event-type badge (1–5) ─────────────────────────────────────────────────
export type EventType = 1 | 2 | 3 | 4 | 5;

const EVENT_TYPE: Record<EventType, { tone: Tone; key: MessageKey }> = {
  1: { tone: "tone-info", key: "eventType.buyBox" },
  2: { tone: "tone-accent", key: "eventType.competitorOffer" },
  3: { tone: "tone-ink2", key: "eventType.sellerCount" },
  4: { tone: "tone-conflict", key: "eventType.priceBoundary" },
  5: { tone: "tone-risk", key: "eventType.marginFloor" },
};

export function EventTypeBadge({ type }: { type: EventType }) {
  const t = useT();
  const m = EVENT_TYPE[type];
  return <Badge tone={m.tone} label={t(m.key)} shape="none" />;
}

// ── Freshness pill (fresh ≤60m / aging 1–6h / stale >6h) ───────────────────
export function FreshnessPill({ ageMinutes }: { ageMinutes: number }) {
  const t = useT();
  const band: { tone: Tone; key: MessageKey } =
    ageMinutes <= 60
      ? { tone: "tone-pos", key: "freshness.fresh" }
      : ageMinutes <= 360
        ? { tone: "tone-warn", key: "freshness.aging" }
        : { tone: "tone-risk", key: "freshness.stale" };
  return <Badge tone={band.tone} label={t(band.key)} />;
}
