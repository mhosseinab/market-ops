"""P0 chat flows (PRD §6.8–6.11, §8.3, §8.5) — deterministic, model-free logic.

Each journey (7–10) is a pure function or small cohesive class over *typed*
inputs the orchestrator already fetched from the read/Draft-only tools. The model
never decides any of the load-bearing facts here:

* **briefing** — order/counts come from the Today ranking, byte-identical to the
  feed (CHAT-010/011);
* **investigation** — chat filters compile to the SAME canonical query string the
  screens use (CHAT-033);
* **simulation** — a labelled, non-executable result that structurally cannot
  carry an approval control (CHAT-032);
* **prepare / bulk** — the ONLY write is a Draft (or a versioned selection set)
  via the Draft-only port; nothing advances past Draft, and there is no
  chat-owned confirm/approve/bulk-approve path (CHAT-040–051);
* **admin** — Level-1 reads relay Settings values, Level-2 emits a
  before/after/scope/consequence proposal Draft, Level-3 is explanation +
  deep-link only (CHAT-060/061/062);
* **blockers** — ordered byte-identically to the policy engine, resolved one at a
  time (CHAT-070/071/072);
* **monitoring** — grouped by terminal state, retry blocked while unreconciled
  (CHAT-073/074).

The single write the model plane can originate is :class:`TransitionKind.DRAFT`.
There is deliberately no approve/execute/confirm member — the structural
prohibition (§12.3) is enforced by the *absence* of the capability, not a guard.
"""

from __future__ import annotations
