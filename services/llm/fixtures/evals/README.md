# LLM-plane eval fixtures (§12.5) — S21 seed + S24 completion

These JSONL files are the offline eval corpus the harness (`llm.evals`, step S24)
loads. S21/S23 **author** the intent/context/adversarial sets; S24 authors the
remaining §12.5 sets (plus the beyond-minimum injection set) and **measures** every
§12.5 exit threshold. No threshold is asserted at authoring time.

## Contents

| Path | Cases | Exercises |
|---|---|---|
| `intents/intents_{fa,en,mixed}.jsonl` | 200 | intent classification (PRD §8.2, CHAT-080/081) |
| `context/context_{ambiguous,resolved}.jsonl` | 100 | deterministic resolution + CHAT-007 containment |
| `adversarial/approval.jsonl` | 60 (≥50) | free-text approval containment (CHAT-041) |
| `pricing/pricing_events.jsonl` | 100 | factual support on pricing events (CHAT-020) |
| `data_quality/data_quality.jsonl` | 50 | missing/stale/conflicted evidence handling |
| `boundary/boundary.jsonl` | 50 | floor / movement-cap conflict answers |
| `listing/listing_diagnostic.jsonl` | 50 | listing diagnostics (observed field + rule version) |
| `currency/currency_ambiguity.jsonl` | 30 | §9.1 quarantine-over-inference |
| `injection/injection.jsonl` | 20 | data-channel injection (hostile evidence text) |

The harness runs against ANY configured OpenAI-compatible provider
(`uv run python -m llm.evals --provider mock --suite all --report out.json`) and
separates **architectural containment gates** (adversarial, injection,
ambiguous-context, currency, and the malicious-provider fuzz — all 100% on the
mock) from **measured accuracy** (intent/context/factual/cost — a
harness-correctness signal on the mock; the Gate 0a bars are cleared by the
deferred S35 paid benchmark, never a paid call in CI). The report JSON feeds
`dk-p0-plan.md` §11.

The pricing/data-quality/boundary/listing sets are **factual-support** cases: each
carries the typed, sourced evidence a real turn would assemble plus an `expected`
disposition (`supported` grounds a §12.2 envelope; `fail_closed` refuses). The
harness composes each through the REAL `compose_or_refuse` path and scores the
disposition-match rate.

## Schemas

Intent case:
```
{"id", "message", "lang": "fa|en|mixed", "expected_intent": <one of 8 classes>,
 "shorthand": bool, "pending_native_review": bool}
```

Context case (maps 1:1 onto `llm.contextres.ResolveRequest` plus `expected`):
```
{"id", "message", "lang", "intent",
 "scope": {"organization_id", "account_id"},   # authenticated request scope (PRD §12)
 "active_context", "references", "candidates",
 "time_phrase", "now", "ambiguous": bool,
 "expected": {"kind": "resolved|picker|not_found", "context_type": <or null>}}
```
`scope` is the authenticated tenant the turn runs under. Each candidate and the
`active_context` chip carry their own `organization_id` + `account_id` provenance;
the resolver validates that provenance against `scope` before resolving, so a
candidate or chip from another organization/account fails closed (never a
relabeled chip). This single-tenant seed uses `org-1`/`acc-1` throughout; the
cross-tenant negative cases live in `tests/test_context_resolver.py`.

Every `ambiguous: true` context case is card-leading and MUST resolve to a
`picker` — the CHAT-007 containment set. `context/context_ambiguous.jsonl` holds
exactly these; zero of them may resolve to a specific-entity chip.

## Provenance

Regenerate with:
```
uv run python services/llm/fixtures/evals/authoring.py             # intents + context
uv run python services/llm/fixtures/evals/adversarial/authoring.py # approval containment
uv run python services/llm/fixtures/evals/s24_authoring.py         # pricing/data-quality/
                                                                   # boundary/listing/
                                                                   # currency/injection
```
Each authoring script is the single source for the JSONL it emits; edit the
script, not the JSONL, then regenerate. The `s24_authoring.py` factual sets carry
compact typed shapes that `llm.evals.scenario` rebuilds into the real contract
types before composing — see that module for the shape → envelope mapping.

## PENDING NATIVE REVIEW (LOC-003)

Every Persian and mixed-script string here is authored idiomatically by the
implementing agent and is marked `pending_native_review: true`. A native Persian
operator review (LOC-003, `persian_localization_ux`) is a **downstream gate**
before beta; until it passes, treat the fa/mixed linguistic quality of these
fixtures as provisional. English cases are not gated on that review.
