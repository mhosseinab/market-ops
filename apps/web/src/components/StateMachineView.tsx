import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";
import type { ApprovalInvalidationReason, ApprovalState } from "../data/types";
import { LtrToken } from "./LtrToken";

// StateMachineView (component inventory): renders the §8.4 approval lifecycle as
// the surface state — never a model claim. It shows the current stage, the eight
// revalidation gates while Revalidating, and the branch panels for Invalidated /
// Expired / permission-denied / recommend-only. Every label is a catalog key; the
// state→label/tone tables are DATA, not locale/direction branches.

type Tone = "pos" | "risk" | "warn" | "info" | "ink2" | "conflict";

const STATE_LABEL: Record<ApprovalState, MessageKey> = {
  draft: "state.draft",
  ready_for_review: "state.readyForReview",
  blocked: "state.blocked",
  awaiting_confirmation: "state.awaitingConfirmation",
  approved: "sm.recommendOnly.title",
  expired: "state.expired",
  invalidated: "state.invalidated",
  revalidating: "state.revalidating",
  executing: "state.executing",
  accepted: "state.accepted",
  rejected: "state.rejected",
  pending_reconciliation: "state.pendingReconciliation",
  failed: "state.failed",
};

const STATE_TONE: Record<ApprovalState, Tone> = {
  draft: "ink2",
  ready_for_review: "info",
  blocked: "risk",
  awaiting_confirmation: "ink2",
  approved: "info",
  expired: "ink2",
  invalidated: "warn",
  revalidating: "info",
  executing: "info",
  accepted: "pos",
  rejected: "risk",
  pending_reconciliation: "warn",
  failed: "risk",
};

// The eight EXE-001 revalidation gates, in order (design screen 5 checklist).
const REVALIDATION_GATES = [
  "sm.gate.identity",
  "sm.gate.cost",
  "sm.gate.price",
  "sm.gate.evidence",
  "rec.guardrail.floor",
  "sm.gate.movement",
  "sm.gate.version",
  "sm.gate.idempotency",
] as const satisfies readonly MessageKey[];

// A gate is a catalog key from the revalidation list; its authoritative result is
// one of three states. FAIL-CLOSED INVARIANT: the parent lifecycle state
// (state === "revalidating") NEVER maps to a pass. A gate is "passed"/"failed"
// ONLY when an explicit authoritative per-gate result is supplied via gateResults;
// with no result the gate stays "pending" (unknown) — the server runs the matrix
// server-side and streams no per-gate progress in P0, so pending is the truth.
type GateKey = (typeof REVALIDATION_GATES)[number];
type GateStatus = "pending" | "passed" | "failed";

const GATE_STATUS_LABEL: Record<GateStatus, MessageKey> = {
  pending: "sm.gate.status.pending",
  passed: "sm.gate.status.passed",
  failed: "sm.gate.status.failed",
};

// Decorative glyph only (aria-hidden); the accessible status is the catalog label.
const GATE_STATUS_MARK: Record<GateStatus, string> = {
  pending: "○",
  passed: "✓",
  failed: "✕",
};

const REASON_LABEL: Record<Exclude<ApprovalInvalidationReason, "">, MessageKey> = {
  action_mismatch: "approvalReason.action_mismatch",
  parameter_version_changed: "approvalReason.parameter_version_changed",
  context_version_changed: "approvalReason.context_version_changed",
  policy_version_changed: "approvalReason.policy_version_changed",
  cost_version_changed: "approvalReason.cost_version_changed",
  evidence_version_changed: "approvalReason.evidence_version_changed",
  expired: "approvalReason.expired",
};

export function StateMachineView({
  state,
  reason = "",
  executionPending = false,
  permissionDenied = false,
  idempotencyKey,
  gateResults,
  onRecalculate,
  onRequestOwner,
}: {
  state: ApprovalState;
  reason?: ApprovalInvalidationReason;
  executionPending?: boolean;
  permissionDenied?: boolean;
  idempotencyKey?: string;
  // Authoritative per-gate results, supplied ONLY from a server-confirmed source.
  // Absent (the P0 reality) ⇒ every gate renders pending; a lifecycle state can
  // never be inferred as a pass. Never fabricate this from ApprovalState.
  gateResults?: Partial<Record<GateKey, GateStatus>>;
  onRecalculate?: () => void;
  onRequestOwner?: () => void;
}) {
  const t = useT();
  const label =
    state === "accepted"
      ? t("state.accepted", { marketplace: t("marketplace.name") })
      : t(STATE_LABEL[state]);

  return (
    <section className="panel state-machine" data-testid="state-machine">
      <div className="panel__head">
        <h2 className="panel__title">{t("sm.title")}</h2>
        <span className="sm-state" data-tone={STATE_TONE[state]} data-state={state}>
          <span className="badge__dot" aria-hidden />
          {label}
        </span>
      </div>

      {permissionDenied ? (
        <div className="banner banner--risk" role="alert" data-testid="permission-denied">
          <div className="banner__body">
            <p className="banner__title">{t("sm.permission.title")}</p>
            <p className="banner__text">
              {t("sm.permission.body", { role: t("sm.permission.role.owner") })}
            </p>
          </div>
          {onRequestOwner ? (
            <div className="banner__actions">
              <button type="button" className="btn btn--sm" onClick={onRequestOwner}>
                {t("sm.permission.request")}
              </button>
            </div>
          ) : null}
        </div>
      ) : state === "revalidating" ? (
        <div className="sm-gates">
          <p className="panel__subtitle">{t("sm.gates.title")}</p>
          <ul className="sm-gates__list">
            {REVALIDATION_GATES.map((gate) => {
              // Fail closed: default to pending; a pass/fail is honoured ONLY from
              // an explicit authoritative result, never from the lifecycle state.
              const status: GateStatus = gateResults?.[gate] ?? "pending";
              return (
                <li key={gate} className="sm-gates__item" data-gate-status={status}>
                  <span className="sm-gates__mark" aria-hidden>
                    {GATE_STATUS_MARK[status]}
                  </span>
                  {t(gate)}
                  <span className="sm-gates__status">{t(GATE_STATUS_LABEL[status])}</span>
                </li>
              );
            })}
          </ul>
        </div>
      ) : state === "invalidated" ? (
        <div className="banner banner--warn" role="alert" data-testid="invalidated">
          <div className="banner__body">
            <p className="banner__title">{t("sm.invalidated.title")}</p>
            <p className="banner__text">{t("sm.invalidated.body")}</p>
            {reason !== "" ? <p className="banner__text">{t(REASON_LABEL[reason])}</p> : null}
          </div>
          {onRecalculate ? (
            <div className="banner__actions">
              <button type="button" className="btn btn--sm" onClick={onRecalculate}>
                {t("rec.action.recalculate")}
              </button>
            </div>
          ) : null}
        </div>
      ) : state === "expired" ? (
        <div className="banner banner--warn" role="alert" data-testid="expired">
          <div className="banner__body">
            <p className="banner__title">{t("sm.expired.title")}</p>
            <p className="banner__text">{t("sm.expired.body")}</p>
          </div>
          {onRecalculate ? (
            <div className="banner__actions">
              <button type="button" className="btn btn--sm" onClick={onRecalculate}>
                {t("rec.action.recalculate")}
              </button>
            </div>
          ) : null}
        </div>
      ) : state === "approved" ? (
        <div className="sm-terminal" data-testid="recommend-only">
          <p className="success-note">{t("sm.recommendOnly.title")}</p>
          <p className="muted">{t("sm.recommendOnly.body")}</p>
          {executionPending ? <p className="muted">{t("sm.executionPending")}</p> : null}
          {idempotencyKey ? (
            <p className="muted">
              <LtrToken text={idempotencyKey} />
            </p>
          ) : null}
        </div>
      ) : state === "accepted" ? (
        <div className="sm-terminal" data-testid="accepted">
          <p className="success-note">{t("sm.accepted.outcomeWindow")}</p>
          {idempotencyKey ? (
            <p className="muted">
              {t("sm.accepted.audit", { ref: "" })}
              <LtrToken text={idempotencyKey} />
            </p>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}
