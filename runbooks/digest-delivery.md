# Runbook — Daily email digest delivery

**Failure domain:** Per-(account, business day) daily email digest delivery
(NOT-001 / §6.8; `notification_digest_deliveries`, River queue `digest_account`).
**Owning Operations queue (OPS-002):** *none by design.* The digest is **advisory
UI** — the lowest priority in the load-shedding order (approval path > audit append
> reconciliation > observations > advisory UI) — and every digest item is already
readable in-app under the **same shared event id**, so a failed digest blocks no P0
journey. Digest-side triage runs through `operations.queue.staleTargets`
(Operations → "Stale targets"), the same triage route the LLM-outage runbook uses.
**Alert:** none owns this domain. `BriefingGenerationFailure`
(`deploy/prometheus/rules/dk-p0-alerts.yml`) is the adjacent signal — the digest
LINKS to the daily briefing, so a briefing outage and a digest backlog often appear
together. A dedicated digest-backlog alert is §20.1/S33 work and is **not** implied
by this runbook.
**Dashboards:** `DK · Unit economics` (briefing/digest cost), plus the digest
delivery-state roll-up over `notification_digest_deliveries`.

## Why there is no blocking queue

Execution and safety failures **bypass the digest entirely** and are delivered
immediately through the urgent outbox (NOT-001), so no urgent notification can be
lost behind a digest failure. A digest that never arrives is a degraded
convenience, not a blocked journey: the in-app item carries the same event id and
the briefing deep link is reachable from the screens. Inventing a digest Operations
queue would put advisory work in front of the queues that own genuinely blocked
journeys.

## Delivery states

`notification_digest_deliveries` is the delivery-state projection (the ONLY mutable
surface; the digest header and item membership stay append-only).

| State | Terminal | Meaning |
|---|---|---|
| `pending` | no | Discovered, or an attempt failed before/outside the ambiguous window. Definitively not delivered; safe to retry. |
| `sending` | no | A send is in flight, or an attempt was abandoned while holding the claim. `ambiguous` decides how it resolves. |
| `delivered` | yes | The relay accepted the message AND the durable write landed. |
| `skipped` | yes | The closed day held nothing sendable (empty day, or every row isolated). Not a failure. |
| `dead_letter` | yes | Permanent failure or exhausted attempts. **Definitively NOT delivered.** |
| `unconfirmed` | yes | **Ambiguous:** the body was transmitted and acceptance could never be established. Does NOT claim delivery and is **never resent**. |

The `ambiguous` column is the durable post-DATA marker. It is raised by the mailer
at the moment the exchange is about to await the relay's verdict, so a row
abandoned **before** that point is released to `pending` and retried, while one
abandoned **after** it is finalized `unconfirmed`. Never resend an `unconfirmed`
row by hand: at-most-once for the ambiguous window is a deliberate product choice
(a duplicate delivery must never create a duplicate product event, NOT-001).

## Symptom

- A tenant reports no daily digest while other tenants received theirs.
- Rows accumulating in `dead_letter` or `unconfirmed` for one account/day.
- Rows sitting in `sending` longer than the 15-minute stale window.
- `notify.digest.account_failed` climbing, or repeated ERROR logs
  `digest DEAD-LETTERED …` / `digest finalized UNCONFIRMED …`.

## Ownership boundary

Delivery-state correctness, idempotency, and the append-only header/membership are
owned by `go_domain_executor`. Platform owns the queue bound, the job/attempt
telemetry, and this runbook. The SMTP relay itself is a deploy-time concern.

## Diagnosis

Everything needed is on the row — all bounded technical identifiers, never relay
text and never a recipient address.

```sql
SELECT business_day, delivery_state, ambiguous, attempts,
       last_reason, last_status_code, updated_at, finalized_at
  FROM notification_digest_deliveries
 WHERE marketplace_account_id = :account
 ORDER BY business_day DESC
 LIMIT 20;
```

1. **`dead_letter` with `last_reason = unsendable_target` / `resolve_error` /
   `render_error`** — a tenant-configuration problem, not an infrastructure one. Fix
   the recipient/locale/catalog input; the row stays terminal (see Recovery).
2. **`dead_letter` with `smtp_permanent_rejection` (`last_status_code` 5xx)** — the
   relay refused the recipient. Nothing was transmitted.
3. **`dead_letter` with `attempts_exhausted`** — the bounded per-account retry
   budget ran out against a transient failure (`smtp_transient_rejection`,
   `smtp_timeout`, `smtp_dial_failed`). Check the relay before re-driving.
4. **`unconfirmed`** — a genuinely ambiguous send. Confirm against the RELAY's own
   logs whether the message landed. Do **not** resend from here.
5. **`sending` older than 15 minutes** — an attempt was abandoned. `ambiguous =
   false` means the recovery pass will release and retry it; `ambiguous = true`
   means it will finalize `unconfirmed`. Both happen automatically.
6. **`send_not_initiated`** — the previous attempt was abandoned before
   transmission and the claim was released for retry. Expected after a restart
   during the digest window; a rising count means restarts are landing mid-send.
7. **`send_window_unrecordable`** — the ambiguous window could not be recorded, so
   the send was abandoned before entering it (fail closed). This means the database
   was unavailable mid-send; treat it as a database incident, not a mail incident.
8. **Backlog rather than failure** — the digest runs on its own bounded queue
   (`digest_account`, 3 workers). Confirm work is progressing:

```sql
SELECT delivery_state, count(*) FROM notification_digest_deliveries
 WHERE delivery_state IN ('pending','sending') GROUP BY 1;
```

## Recovery

1. **Do nothing first.** Recovery is OWNED and automatic: every fan-out pass
   re-enqueues every nonterminal row of any historical day directly from the
   delivery table (`DigestService.RecoverNonterminal`), consulting no River state
   and pinning each row's original business day. A `pending` or stale `sending` row
   repairs itself without operator action.
2. **A terminal row is never re-driven.** `delivered`, `skipped`, `dead_letter`, and
   `unconfirmed` are invisible to recovery — that is the zero-resend guarantee.
3. **To retry a `dead_letter` row after fixing its cause** (a corrected recipient, a
   relay fix), re-open exactly that row and let the normal recovery pass drive it:

```sql
UPDATE notification_digest_deliveries
   SET delivery_state = 'pending', ambiguous = false, finalized_at = NULL,
       last_reason = 'operator_redrive', updated_at = now()
 WHERE marketplace_account_id = :account AND business_day = :day
   AND delivery_state = 'dead_letter';
```

   Scope it to the one `(account, business_day)` — a blanket update re-drives every
   tenant. The append-only digest header and item membership are reused verbatim, so
   the retry covers the SAME window with the SAME items.
4. **Never re-open an `unconfirmed` row.** Confirm delivery from the relay's logs
   instead. If the tenant genuinely did not receive it, that is a product decision
   (at-most-once vs at-least-once for the ambiguous window), not an operator action.
5. **A stuck job that keeps re-driving** (the terminal state write itself failing)
   releases its slot after the bounded re-drive window and is discarded by River with
   `digest terminal re-drive window elapsed`. The durable row stays nonterminal and
   the owned recovery pass still owns the repair — fix the database, then let
   recovery run.

## Exit

- No row older than one business day remains in `pending` or `sending`.
- `dead_letter` / `unconfirmed` counts flat, each with a bounded `last_reason` an
  operator can act on.
- The affected tenants' next scheduled digest reaches `delivered`.
