# Localization / i18n Architecture — DK Command Center

Localization is a **first-class architectural requirement**: platform-, model-, and region-agnostic. Nothing about language, direction, digits, currency, or marketplace is hardcoded — all are driven by a locale config + a string dictionary. The prototype ships Persian-first (default `locale:'fa'`) with a reachable English/LTR toggle in the top bar that flips the whole app.

## Locale config (single source)
Each locale is an object; switching `locale` re-derives everything:
```
LOCALES = {
  fa: { dir:'rtl', lang:'fa', digits:'fa',   sep:'٬', cur:'تومان', name:'فارسی'  },
  en: { dir:'ltr', lang:'en', digits:'latn', sep:',', cur:'Toman', name:'English' },
}
```
- `dir` → set on the app root (`dir={{dir}}`); the whole layout mirrors because the UI uses **logical CSS properties** (`border-inline-*`, `margin-inline-*`, `inset-inline-*`, `text-align:start`), never left/right.
- `digits` → `fa` renders ۰۱۲۳؛ `latn` renders 0123. All numbers pass through one `digits()`/`fa()` helper, so every price, count, percentage, and freshness value localizes automatically everywhere.
- `sep` → grouping separator (`٬` vs `,`).
- `cur` → currency label appended by `money()`/`toman()`. Currency is a **display label + conversion**, not a hardcoded unit — extend the config with `code`, `symbol`, `toDisplay(rawMinorUnit)` per region (e.g. Rial→Toman ÷10 today; a different market supplies its own).

## Marketplace is a parameter, not a constant
`MKT = { fa:'دیجی‌کالا', en:'Digikala' }` — interpolated into brand/connection strings. A second connector/region swaps this (and the connector capability contract) without touching UI. Do **not** hardcode "Digikala"/"DK" in components; read from config.

## String dictionary + resolvers
- `DICT[key] = { fa, en }` — every UI chrome string. Resolve via `t(key)`; missing key falls back to `fa` (and in a real build, logs for translation coverage).
- `TR = { faString: enString }` — maps data-driven labels (quality/status/readiness/event-type metas defined once in Persian) to their English equivalents; resolved via `tx(faLabel)`. This keeps the canonical Persian glossary (§13.5 of the PRD) as the source and derives other locales.
- In a production stack use a standard i18n library (ICU MessageFormat / i18next / FormatJS) with **pluralization and interpolation** — several strings here embed counts (e.g. "3 items missing cost" / «۳ کالا فاقد بها»); those must be plural-aware per locale, not string-concatenated.

## What localizes today (in the prototype)
Direction (full RTL↔LTR mirror), all digits, currency label + separators, and every **quality / observation-state / execution-state / readiness / event-type badge**, plus nav, route titles, top-bar controls, blockers, density, chat context chip, and the state simulator.

## Remaining i18n work (queued — implement the same way)
Convert the remaining **static template literals** (some Today headings/CTAs, deep-screen section titles) and **deep-screen mock-data strings** (operations queue text, onboarding steps, scope names, chat briefing body, action product names) to `DICT`/`TR` keys. These were left Persian in the prototype to stay focused; the pattern is identical and mechanical. A production build should have **zero string literals in components** — everything through `t()`.

## RTL / LTR mixed-content rules (both directions)
- Persian/Arabic and English can be the base direction; **technical identifiers are always LTR-isolated**: SKUs, URLs, model numbers, IDs use `direction:ltr; unicode-bidi:isolate` (monospace). This holds in both fa and en.
- Tables mix RTL text columns with LTR identifier columns using `text-align:start` and isolated cells — no bidi corruption.
- Dates/numbers: format per `Intl` with the active locale; the prototype uses a manual digit map for the demo — replace with `Intl.NumberFormat`/`Intl.DateTimeFormat` (and a Jalali calendar option for fa).

## Implementation checklist for Claude Code
1. Pick an i18n lib; load locale bundles; expose `locale`, `dir`, `t()` via context/provider.
2. Set `dir`/`lang` on the document root from locale; audit CSS for any physical left/right → convert to logical.
3. Route ALL user-facing numbers/currency/dates through locale-aware `Intl` formatters; never concatenate digits/units.
4. Keep the canonical state glossary as the source; derive other locales; enforce with a copy-lint (PRD CHAT-084).
5. Parameterize marketplace name + currency + calendar from config; no hardcoded region assumptions.
6. Add pluralization/interpolation for count strings; add a per-user response-language setting (PRD Q8).
