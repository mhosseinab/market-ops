# Runbook — Action reconciliation backlog

**Failure domain:** Execution reconciliation (EXE-002/003, AUD-001).
**Owning Operations queue (OPS-002):** `operations.queue.pendingRecon`
(Operations screen → "Pending reconciliation"; web deep link `/docs/runbooks/reconciliation`).
**Alert:** `ReconciliationBacklog` (`deploy/prometheus/rules/dk-p0-alerts.yml`).
**Dashboards:** `DK · Approval & execution integrity`, `DK · Outcomes & confidence`.

## Symptom

- Alert `ReconciliationBacklog` firing: over 30m, `execution_pending_reconciliation`
  parks exceed `execution_terminal_results` reconciliations.
- Operations → "Pending reconciliation" queue surfaced (contract list endpoint not
  yet exposed ⇒ explicit unavailable count, never a fabricated zero).
- Action acknowledgement missing the §17.2 target (state visible within 30s).

## Owning queue and ownership boundary

Money/policy/approval/execution correctness is owned by `go_domain_executor`.
Platform owns the reconciliation backlog alert, the queue mapping, and the
integrity telemetry — it does not redefine the write/reconcile logic.

## Diagnosis

1. On `DK · Approval & execution integrity`, compare "External write attempts (by
   state)" against "Pending reconciliation (30m)". A growing `pending_reconciliation`
   with flat `terminal_results` means unknown write results are not resolving.
2. **Audit integrity first (never-cut):** confirm the "Audit-write failures (must be
   0)" stat is 0. A non-zero value means an audit append forced a rollback — that is
   a page-worthy incident that supersedes the backlog; do not proceed until it is 0.
3. Confirm every parked action carries a stable idempotency key. A retry without a
   stable key is a bug, not recovery — reconciliation must be idempotent.
4. Determine why results are unknown: DK acknowledgement delayed (upstream) vs the
   reconciliation pass not running (River job stalled). Check the River queue depth /
   backpressure signal; a stalled worker is not a DK problem.

## Recovery

1. **Reconciliation pass stalled:** confirm the River recommend-only/outcome pipeline
   is running (transactionally enqueued from Go). Restart the worker pipeline if
   stopped; parked results reconcile to terminal states on the next pass.
2. **DK acknowledgement delayed:** leave results parked pending; they reconcile when
   DK confirms. Never coerce an unknown result to success (quarantine over inference).
3. **Write enablement off (dark):** parked actions lapse to recommend-only after
   their window; confirm the account is visibly recommend-only. This is the honest
   fail-closed state until the gated S35 write probes record verified parameters.
4. Confirm approval-control versioning survived every retry (action ID + parameter
   version + context version on the trace). Loss of versioning across a retry is a
   never-cut bug.

## Exit

`ReconciliationBacklog` resolved, "Pending reconciliation" draining to terminal
states on the outcomes dashboard, audit-write failures at 0, and action
acknowledgement back within §17.2 (30s).
