# State Matrix — DK Command Center (P0)

Legend: **✔ built** in the prototype · **◐ partial** (represented but not fully interactive) · **○ documented, not built** (implement per spec).

**Global state simulator:** the top bar has a `حالت: واقعی / بارگذاری / خالی` control (`state.demo`) that renders a generic **loading skeleton** and **empty state** over ANY screen — so initial-loading and empty are covered app-wide by one shared pattern; per-screen tailored versions (e.g. Today's own no-action/disconnected) sit on top. Implement loading/empty as shared wrapper components keyed off a per-view fetch status.

## Per-screen state coverage

| State | Today | Product detail | Event detail | Recommendation/Approval | Bulk | Cost import | Actions | Market | Notes |
|---|---|---|---|---|---|---|---|---|---|
| Initial loading | ✔ | ✔* | ✔* | ✔* | ✔* | ✔ idle | ✔* | ✔* | ✔* = via global simulator; Today also has a tailored skeleton. Reuse pulse pattern (`@keyframes pulse`, `--panel` blocks) |
| Empty / No-action-needed | ✔ | ✔* | ✔* | — | ✔* | ✔ idle | ✔* | ✔* | Global empty covers all; Today "هیچ اقدامی لازم نیست" is the canonical reassuring pattern |
| Ready | ✔ | ✔ | ✔ | ✔ | ✔ | ✔ | ✔ | ✔ | |
| Partial data | ◐ | ✔ (readiness=جزئی) | — | ○ | ◐ | — | — | ✔ | Partial → analysis only, never executable |
| Missing cost | ✔ blocker chip | ✔ blocked banner | — | ○ | ✔ blocked row | ✔ | — | — | Blocks margin + price action |
| Stale cost | ○ | ◐ | — | ○ | ○ | — | — | — | Expire recommendation; request refresh |
| Unresolved identity | ✔ blocker chip | ○ | — | — | — | ◐ review row | — | — | Needs-Review mapping quarantined; Operations queue |
| Unverified observation | ✔ (quality dot) | ✔ | ◐ | — | — | — | — | ✔ | May display, cannot recommend/execute |
| Conflicted observation | ✔ (event row) | ✔ | ◐ | — | ◐ blocked | — | — | ✔ banner + diagnosis | Blocks recommendation; routes to Operations |
| Stale observation | ✔ (event row) | ✔ | — | — | ✔ blocked | — | — | ✔ | Last value + age only; blocks dependent action |
| Connector disconnected | ✔ | ○ | ○ | ○ | ○ | ○ | ○ | ○ | Today shows full disconnected state; read-only from last sync, labeled with age |
| Permission / capability missing | — | — | — | ✔ | ○ | — | — | — | Approval card permission-denied stage: names required role (مالک), no silent downgrade, offers "درخواست تایید از مالک". Settings shows L2/L3 level tags |
| Recommendation blocked | ✔ (Today panel) | ✔ | ✔ | ◐ | ✔ | — | — | — | Deterministic block reasons in policy order |
| Recommendation expired | — | — | — | ✔ | ○ | — | — | — | Approval card "منقضی‌شده" → recalculate |
| Permission-denied (approval) | — | — | — | ✔ | — | — | — | — | "مجوز کافی ندارید" — required role named, action stopped |
| Processing / executing | — | — | — | ✔ (revalidating→executing) | ✔ | ◐ | ◐ | — | Platform-sourced state stream, not model claims |
| Partial batch failure | — | — | — | — | ✔ per-item results | — | ✔ | — | Per-item terminal states; retry only eligible |
| Pending reconciliation | — | — | — | ○ | ✔ | — | ✔ detail panel | — | Never shown as success/failure; no retry until DK state read |
| Completed / Accepted | — | — | — | ✔ | ✔ | ✔ | ✔ | — | "تاییدشده توسط دیجی‌کالا" + audit + outcome window |
| Externally executed (recommend-only) | — | — | — | ○ | — | — | ✔ (row) | — | Connector observes matching owned-price change within 24h |

## Approval / action state machine (canonical — shared by chat & screens)
Implemented on the Recommendation screen; the same machine must back the Actions records and any chat approval card.

```
Draft ──validate──▶ ReadyForReview ──open card──▶ AwaitingConfirmation
                                        │  (structured control tapped)
AwaitingConfirmation ──▶ Approved ──▶ Revalidating ──gates pass──▶ Executing
        │ expiry ▶ Expired                    │ precondition changed ▶ Invalidated
        │ evidence/param changed ▶ Invalidated
Executing ──▶ Accepted | Rejected | PendingReconciliation | Failed
PendingReconciliation ──reconciled──▶ Accepted | Failed
Invalidated ──recalculated──▶ Draft (new card, new version)
```
Rules the UI must enforce visually: free text never approves; approval control is bound to action ID + parameter version + expiry; editing the proposed price creates a new version and voids the prior control; duplicate taps rejected by idempotency key; on restore, pending cards re-fetch state and expired cards render as expired.

## Observation quality → capability (drives every quality badge)
| Quality | Display | Recommend | Execute after approval |
|---|---|---|---|
| Verified تاییدشده | yes | yes | yes (if all gates pass) |
| Supported پشتیبانی‌شده | yes | yes | only after JIT refresh |
| Unverified تاییدنشده | yes | no | no |
| Conflicted متناقض | yes (warning) | no | no |
| Stale قدیمی‌شده | last value + age only | no | no |
| Unavailable در دسترس نیست | state only | no | no |

## Margin readiness → pricing consequence
| Readiness | Consequence |
|---|---|
| Complete کامل | executable recommendation may proceed |
| Partial جزئی | analysis only (labeled) |
| Stale قدیمی‌شده | block + request refresh |
| Missing فاقد بها | block |
