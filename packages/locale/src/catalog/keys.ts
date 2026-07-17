// The message-key contract. Every catalog (fa-IR, en, and the generated pseudo
// pack) is a Record over exactly these keys, so TypeScript enforces coverage and
// the copy-lint / missing-key telemetry (LOC-004) have a closed key set to check.
// Keys carry NAMED ICU slots only — never positional, never concatenated
// (LOC-002). Canonical state terms map 1:1 to PRD §11.4 / the design glossary.

export const MESSAGE_KEYS = [
  // App chrome
  "app.name",
  "app.langName.fa",
  "app.langName.en",
  "brand.mark",
  "marketplace.name",

  // Navigation
  "nav.group.workspace",
  "nav.group.reference",
  "nav.today",
  "nav.products",
  "nav.market",
  "nav.actions",
  "nav.settings",
  "nav.operations",
  "nav.onboarding",
  "nav.ds",

  // Route titles + subtitles (TopBar)
  "route.today.title",
  "route.today.sub",
  "route.products.title",
  "route.products.sub",
  "route.market.title",
  "route.market.sub",
  "route.actions.title",
  "route.actions.sub",
  "route.settings.title",
  "route.settings.sub",
  "route.operations.title",
  "route.operations.sub",
  "route.onboarding.title",
  "route.onboarding.sub",
  "route.ds.title",
  "route.ds.sub",
  "route.event.title",
  "route.event.sub",
  "route.recommendation.title",
  "route.recommendation.sub",
  "route.product.title",
  "route.product.sub",
  "route.cost.title",
  "route.cost.sub",
  "route.bulk.title",
  "route.bulk.sub",
  "route.diagnostics.title",
  "route.diagnostics.sub",

  // TopBar controls
  "topbar.theme.toggle",
  "topbar.density.toggle",
  "topbar.chat.toggle",
  "topbar.connection.healthy",
  "topbar.connection.degraded",
  "topbar.briefingUnseen",

  // Canonical observation / execution state terms (PRD §11.4 — VERBATIM)
  "state.verified",
  "state.supported",
  "state.unverified",
  "state.conflicted",
  "state.stale",
  "state.unavailable",
  "state.blocked",
  "state.awaitingConfirmation",
  "state.executing",
  "state.accepted",
  "state.rejected",
  "state.pendingReconciliation",
  "state.failed",
  "state.expired",
  "state.simulation",

  // Margin readiness (distinct axis)
  "readiness.complete",
  "readiness.partial",
  "readiness.stale",
  "readiness.missing",
  "readiness.missingCount",

  // Event-type badges (1–5)
  "eventType.buyBox",
  "eventType.competitorOffer",
  "eventType.sellerCount",
  "eventType.priceBoundary",
  "eventType.marginFloor",

  // Freshness pill
  "freshness.fresh",
  "freshness.aging",
  "freshness.stale",

  // Currency units (labels only — no unit is ever hardcoded in a view)
  "unit.rial",
  "unit.toman",
  "money.quarantined",

  // Generic screen states (STATE_MATRIX loading/empty/error/degraded)
  "state.loading",
  "state.empty.title",
  "state.empty.body",
  "state.error.title",
  "state.error.body",
  "state.degraded.title",
  "state.degraded.body",

  // Chat dock footnote (free text never executes)
  "chat.footnote",
] as const;

export type MessageKey = (typeof MESSAGE_KEYS)[number];

/** A complete message catalog for one locale. */
export type Catalog = Readonly<Record<MessageKey, string>>;
