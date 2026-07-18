# Runbook — LLM / chat / briefing outage

**Failure domain:** Python LLM plane (chat, daily briefing, model spend).
**Owning Operations queue (OPS-002):** *none by design.* An LLM outage does not
block a P0 journey — chat is best-effort behind a kill switch and **an outage
cannot reduce screen capability** (§17.2). Degraded-briefing surfaces are triaged
via `operations.queue.staleTargets` (Operations → "Stale targets").
**Alerts:** `BriefingGenerationFailure`, `ModelSpendBudgetExhausted`
(`deploy/prometheus/rules/dk-p0-alerts.yml`).
**Dashboards:** `DK · Chat adoption, grounding, latency, cost & containment`,
`DK · Unit economics`.

## Why there is no blocking queue

The LLM plane holds a read + Draft-only credential and no DB access. When it is
down, screens stay fully functional (screens-only fallback, PRD §8). So there is
no "blocked journey" to own in a queue — the operational signal is the alert set
plus the chat kill switch, not an Operations backlog. This is intentional and must
not be "fixed" by inventing an LLM queue.

## Symptom

- Alert `BriefingGenerationFailure`: no successful briefing generation in 25h, or a
  5xx streak on `GET /briefing`.
- Alert `ModelSpendBudgetExhausted`: daily model-attributable variable cost crossed
  the budget on `DK · Unit economics`.
- `/chat` returning `provider_unavailable`, or chat latency breaching §17.2 (first
  token < 3s, read-only completion < 10s) on the chat dashboard.

## Ownership boundary

The LLM agent stack (LangGraph + `create_agent`) is confined to `services/llm`.
Platform owns where its telemetry lands, the kill switch wiring, and these alerts —
not the graph logic. Approval is never a graph interrupt; the structured control
lives outside the model plane, so an LLM outage can never affect an approval.

## Diagnosis

1. Confirm scope: is `/chat` down (provider) or is the whole gateway degraded
   (`DK · SLO / RED overview`)? A gateway-wide problem is a core incident.
2. If provider-down: confirm the circuit breaker guarding the LLM plane opened and
   emitted an audited event (a silent trip is a bug), and that `/chat` fails closed
   to `provider_unavailable` — screens must be unaffected.
3. **Budget pressure (`ModelSpendBudgetExhausted`):** confirm the §17.3 degradation
   ladder engaged **in order**: (1) shorten composition, (2) reuse the already-
   generated daily briefing, (3) minimal-prose structured cards, (4) disable optional
   chat generation and deep-link to screens. A different fallback order is a bug.
4. **Briefing failure:** determine whether generation failed (job) or delivery
   failed. Notification dedup is a delivery-layer guarantee (NOT-001): a duplicate
   delivery must never create a duplicate product event; execution/safety failures
   bypass the digest delay.

## Recovery

1. **Provider outage:** wait for the breaker half-open probe to recover, or flip the
   chat kill switch (CHAT-009) to degrade chat only. Screens and the daily briefing
   read path stay up. No approval or execution is affected.
2. **Budget exhausted:** verify the ladder is holding at the appropriate rung; do not
   raise the budget to paper over it. Optional chat generation stays disabled with a
   deep link to screens until spend normalizes.
3. **Briefing generation failed:** the digest reuses/links the last good briefing
   (ladder step 2). Re-run the briefing job (River, transactionally enqueued) once
   the provider recovers; confirm a `briefing generated` event returns.
4. Confirm free-text containment held throughout: no free-text approve/execute
   attempt produced a transition (the containment panel distinguishes a contained
   attempt from a silent bypass). Free text never approves — never-cut.

## Exit

Both alerts resolved, chat latency back within §17.2 (or chat intentionally killed
with screens fully capable), briefing generation resumed, and model spend back under
budget with the ladder disengaged.
