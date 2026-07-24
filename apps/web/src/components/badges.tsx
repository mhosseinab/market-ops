import type { FreshnessState, MessageKey } from "@market-ops/locale";
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

// ── Listing/image diagnostic result (LST-001, read-only) ───────────────────
// A pass/warn verdict paired with its label — color never stands alone. `warn`
// flags a field needing attention; it triggers no write or auto-fix.
export type DiagnosticResultState = "pass" | "warn";

const DIAGNOSTIC_RESULT: Record<DiagnosticResultState, { tone: Tone; key: MessageKey }> = {
  pass: { tone: "tone-pos", key: "diagnostics.result.pass" },
  warn: { tone: "tone-warn", key: "diagnostics.result.warn" },
};

export function DiagnosticResultBadge({ state }: { state: DiagnosticResultState }) {
  const t = useT();
  const m = DIAGNOSTIC_RESULT[state];
  return <Badge tone={m.tone} label={t(m.key)} />;
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

// ── §8.4 approval-lifecycle state (pre-execution rows) ─────────────────────
// The state of a card that has NOT been executed. It is a THIRD axis, disjoint
// from both write results and recommend-only states, so a pre-execution card can
// never borrow an execution term: `approved` is "approved for execution", never
// "Accepted by DK" (the marketplace's answer to a write that was actually made)
// and never "Awaiting external execution" (which is an EXE-005 tracked state).
export type ApprovalLifecycleState =
  | "draft"
  | "ready_for_review"
  | "blocked"
  | "awaiting_confirmation"
  | "approved"
  | "expired"
  | "invalidated"
  | "revalidating"
  | "executing"
  | "accepted"
  | "rejected"
  | "pending_reconciliation"
  | "failed";

const APPROVAL_LIFECYCLE: Record<ApprovalLifecycleState, { tone: Tone; key: MessageKey }> = {
  draft: { tone: "tone-ink2", key: "state.draft" },
  ready_for_review: { tone: "tone-info", key: "state.readyForReview" },
  blocked: { tone: "tone-risk", key: "state.blocked" },
  awaiting_confirmation: { tone: "tone-ink2", key: "state.awaitingConfirmation" },
  approved: { tone: "tone-info", key: "state.approved" },
  expired: { tone: "tone-ink2", key: "state.expired" },
  invalidated: { tone: "tone-warn", key: "state.invalidated" },
  revalidating: { tone: "tone-info", key: "state.revalidating" },
  executing: { tone: "tone-info", key: "state.executing" },
  accepted: { tone: "tone-pos", key: "state.accepted" },
  rejected: { tone: "tone-risk", key: "state.rejected" },
  pending_reconciliation: { tone: "tone-warn", key: "state.pendingReconciliation" },
  failed: { tone: "tone-risk", key: "state.failed" },
};

export function ApprovalStateBadge({ state }: { state: ApprovalLifecycleState }) {
  const t = useT();
  const m = APPROVAL_LIFECYCLE[state];
  const label =
    state === "accepted" ? t("state.accepted", { marketplace: t("marketplace.name") }) : t(m.key);
  return <Badge tone={m.tone} label={label} />;
}

// ── EXE-005 recommend-only lifecycle (issue #106) ──────────────────────────
// A DELIBERATELY SEPARATE axis from StatusState. A recommend-only action never
// wrote to the marketplace, so no write term may reach it: keeping the maps
// disjoint makes "Accepted by DK" structurally unreachable for a recommend-only
// row, rather than relying on a caller remembering not to pass it. `lapsed` is
// its own neutral term — never `expired` (an approval card) and never `failed`
// (a write that was attempted and failed).
export type RecommendOnlyStatusState =
  | "awaiting_external_execution"
  | "externally_executed"
  | "lapsed";

const RECOMMEND_ONLY: Record<RecommendOnlyStatusState, { tone: Tone; key: MessageKey }> = {
  awaiting_external_execution: { tone: "tone-ink2", key: "state.awaitingExternalExecution" },
  externally_executed: { tone: "tone-pos", key: "state.externallyExecuted" },
  lapsed: { tone: "tone-muted", key: "state.lapsed" },
};

export function RecommendOnlyBadge({ state }: { state: RecommendOnlyStatusState }) {
  const t = useT();
  const m = RECOMMEND_ONLY[state];
  return <Badge tone={m.tone} label={t(m.key)} />;
}

// ── Execution mode (EXE-003 write vs EXE-005 recommend-only) ───────────────
// The mode is an authoritative property of the action, not a visual grouping:
// it is what tells an operator whether anything was written to the marketplace.
export type ExecutionModeName = "write" | "recommend_only";

const EXECUTION_MODE: Record<ExecutionModeName, { tone: Tone; key: MessageKey }> = {
  write: { tone: "tone-info", key: "actions.mode.write" },
  recommend_only: { tone: "tone-accent", key: "actions.mode.recommendOnly" },
};

export function ExecutionModeBadge({ mode }: { mode: ExecutionModeName }) {
  const t = useT();
  const m = EXECUTION_MODE[mode];
  return <Badge tone={m.tone} label={t(m.key)} shape="square" />;
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

// ── Availability (normalized, docs/11) ─────────────────────────────────────
export type AvailabilityState =
  | "in_stock"
  | "out_of_stock"
  | "limited"
  | "unavailable"
  | "disappeared";

const AVAILABILITY: Record<AvailabilityState, { tone: Tone; key: MessageKey }> = {
  in_stock: { tone: "tone-pos", key: "availability.in_stock" },
  out_of_stock: { tone: "tone-risk", key: "availability.out_of_stock" },
  limited: { tone: "tone-warn", key: "availability.limited" },
  unavailable: { tone: "tone-muted", key: "availability.unavailable" },
  disappeared: { tone: "tone-ink2", key: "availability.disappeared" },
};

export function AvailabilityBadge({ state }: { state: AvailabilityState }) {
  const t = useT();
  const m = AVAILABILITY[state];
  return <Badge tone={m.tone} label={t(m.key)} />;
}

// ── Connector capability status (ACC-001; distinct axis from observation) ────
export type CapabilityState = "unknown" | "supported" | "unsupported" | "degraded";

const CAPABILITY: Record<CapabilityState, { tone: Tone; key: MessageKey }> = {
  unknown: { tone: "tone-muted", key: "capabilityState.unknown" },
  supported: { tone: "tone-pos", key: "capabilityState.supported" },
  unsupported: { tone: "tone-risk", key: "capabilityState.unsupported" },
  degraded: { tone: "tone-warn", key: "capabilityState.degraded" },
};

export function CapabilityBadge({ state }: { state: CapabilityState }) {
  const t = useT();
  const m = CAPABILITY[state];
  return <Badge tone={m.tone} label={t(m.key)} shape="square" />;
}

// ── Identity mapping state (CAT-002; S26 Products read model) ───────────────
// The mapping state of a synced variant. A row that is not `confirmed` (and
// watched) can never drive an executable recommendation; the badge only reports
// the state. `unmapped` means the variant has no Market Product Identity yet.
export type MappingStateName = "confirmed" | "needs_review" | "rejected" | "obsolete" | "unmapped";

const MAPPING: Record<MappingStateName, { tone: Tone; key: MessageKey }> = {
  confirmed: { tone: "tone-pos", key: "mapping.confirmed" },
  needs_review: { tone: "tone-warn", key: "mapping.needsReview" },
  rejected: { tone: "tone-risk", key: "mapping.rejected" },
  obsolete: { tone: "tone-ink2", key: "mapping.obsolete" },
  unmapped: { tone: "tone-muted", key: "mapping.unmapped" },
};

export function MappingBadge({ state }: { state: MappingStateName }) {
  const t = useT();
  const m = MAPPING[state];
  return <Badge tone={m.tone} label={t(m.key)} shape="square" />;
}

// ── Cost-import row disposition (CST-001) ───────────────────────────────────
export type DispositionState = "accept" | "reject" | "duplicate";

const DISPOSITION: Record<DispositionState, { tone: Tone; key: MessageKey }> = {
  accept: { tone: "tone-pos", key: "disposition.accept" },
  reject: { tone: "tone-risk", key: "disposition.reject" },
  duplicate: { tone: "tone-warn", key: "disposition.duplicate" },
};

export function DispositionBadge({ state }: { state: DispositionState }) {
  const t = useT();
  const m = DISPOSITION[state];
  return <Badge tone={m.tone} label={t(m.key)} />;
}

// ── Freshness pill (OBS-004) ───────────────────────────────────────────────
// Renders an ALREADY-DERIVED freshness state. Callers derive it from the shared
// source of truth (apps/web/src/data/freshness.ts): offer surfaces via
// `freshnessState(offer, now)` (deadline-driven), event surfaces via
// `freshnessStateFromAge(ageMinutes(...))`. This pill never re-derives from an
// age threshold, so it can never disagree with the extension overlay or the
// action/bulk gates that read the SAME derived state at the SAME instant.
const FRESHNESS: Record<FreshnessState, { tone: Tone; key: MessageKey }> = {
  fresh: { tone: "tone-pos", key: "freshness.fresh" },
  aging: { tone: "tone-warn", key: "freshness.aging" },
  stale: { tone: "tone-risk", key: "freshness.stale" },
};

export function FreshnessPill({ state }: { state: FreshnessState }) {
  const t = useT();
  const band = FRESHNESS[state];
  return <Badge tone={band.tone} label={t(band.key)} />;
}
