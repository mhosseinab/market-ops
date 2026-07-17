# LLM-plane eval fixtures (§12.5) — S21 seed

These JSONL files are the offline eval corpus the harness (step S24) loads. S21
**authors** them; S24 **measures** the §12.5 exit thresholds against them. No
threshold is asserted at authoring time.

## Contents

| Path | Cases | Exercises |
|---|---|---|
| `intents/intents_fa.jsonl` | Persian | intent classification (PRD §8.2, CHAT-080) |
| `intents/intents_en.jsonl` | English | intent classification |
| `intents/intents_mixed.jsonl` | Mixed script + operator shorthand | CHAT-080; Persian/Latin digits (CHAT-081) |
| `context/context_ambiguous.jsonl` | Ambiguous / multi-ref / account-level | CHAT-007 containment (must picker, never a card) |
| `context/context_resolved.jsonl` | Explicit-ref override, in-context, question+time, not-found | deterministic resolution (§8.1) |

Totals: **200** intent cases (25 per class × 8 classes), **100** context cases.

## Schemas

Intent case:
```
{"id", "message", "lang": "fa|en|mixed", "expected_intent": <one of 8 classes>,
 "shorthand": bool, "pending_native_review": bool}
```

Context case (maps 1:1 onto `llm.contextres.ResolveRequest` plus `expected`):
```
{"id", "message", "lang", "intent", "active_context", "references", "candidates",
 "time_phrase", "now", "ambiguous": bool,
 "expected": {"kind": "resolved|picker|not_found", "context_type": <or null>}}
```

Every `ambiguous: true` context case is card-leading and MUST resolve to a
`picker` — the CHAT-007 containment set. `context/context_ambiguous.jsonl` holds
exactly these; zero of them may resolve to a specific-entity chip.

## Provenance

Regenerate with:
```
uv run python services/llm/fixtures/evals/authoring.py
```
`authoring.py` is the single source; edit it, not the JSONL, then regenerate.

## PENDING NATIVE REVIEW (LOC-003)

Every Persian and mixed-script string here is authored idiomatically by the
implementing agent and is marked `pending_native_review: true`. A native Persian
operator review (LOC-003, `persian_localization_ux`) is a **downstream gate**
before beta; until it passes, treat the fa/mixed linguistic quality of these
fixtures as provisional. English cases are not gated on that review.
