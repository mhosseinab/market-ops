import type { MessageKey } from "@market-ops/locale";

// Route + navigation configuration as DATA. SideNav, TopBar, and the router all
// read from here so titles/labels are catalog keys (zero literals) and adding a
// screen is a data edit. Direction/locale never appear here.

export type RouteKey =
  | "today"
  | "products"
  | "market"
  | "actions"
  | "settings"
  | "operations"
  | "onboarding"
  | "ds"
  | "event"
  | "recommendation"
  | "product"
  | "cost"
  | "bulk"
  | "diagnostics"
  | "runbook";

export interface RouteDef {
  readonly key: RouteKey;
  readonly path: string;
  readonly titleKey: MessageKey;
  readonly subKey: MessageKey;
  /** nav group, or undefined for deep-link-only sub-routes. */
  readonly navGroup?: "workspace" | "reference";
  readonly navLabelKey?: MessageKey;
}

export const ROUTES: readonly RouteDef[] = [
  {
    key: "today",
    path: "/today",
    titleKey: "route.today.title",
    subKey: "route.today.sub",
    navGroup: "workspace",
    navLabelKey: "nav.today",
  },
  {
    key: "products",
    path: "/products",
    titleKey: "route.products.title",
    subKey: "route.products.sub",
    navGroup: "workspace",
    navLabelKey: "nav.products",
  },
  {
    key: "market",
    path: "/market",
    titleKey: "route.market.title",
    subKey: "route.market.sub",
    navGroup: "workspace",
    navLabelKey: "nav.market",
  },
  {
    key: "actions",
    path: "/actions",
    titleKey: "route.actions.title",
    subKey: "route.actions.sub",
    navGroup: "workspace",
    navLabelKey: "nav.actions",
  },
  {
    key: "settings",
    path: "/settings",
    titleKey: "route.settings.title",
    subKey: "route.settings.sub",
    navGroup: "workspace",
    navLabelKey: "nav.settings",
  },
  {
    key: "operations",
    path: "/operations",
    titleKey: "route.operations.title",
    subKey: "route.operations.sub",
    navGroup: "workspace",
    navLabelKey: "nav.operations",
  },
  {
    key: "onboarding",
    path: "/onboarding",
    titleKey: "route.onboarding.title",
    subKey: "route.onboarding.sub",
    navGroup: "reference",
    navLabelKey: "nav.onboarding",
  },
  {
    key: "ds",
    path: "/ds",
    titleKey: "route.ds.title",
    subKey: "route.ds.sub",
    navGroup: "reference",
    navLabelKey: "nav.ds",
  },
  { key: "event", path: "/event", titleKey: "route.event.title", subKey: "route.event.sub" },
  {
    key: "recommendation",
    path: "/recommendation",
    titleKey: "route.recommendation.title",
    subKey: "route.recommendation.sub",
  },
  {
    key: "product",
    path: "/product",
    titleKey: "route.product.title",
    subKey: "route.product.sub",
  },
  { key: "cost", path: "/cost", titleKey: "route.cost.title", subKey: "route.cost.sub" },
  { key: "bulk", path: "/bulk", titleKey: "route.bulk.title", subKey: "route.bulk.sub" },
  {
    key: "diagnostics",
    path: "/diagnostics",
    titleKey: "route.diagnostics.title",
    subKey: "route.diagnostics.sub",
  },
  // Deep-link-only: the in-SPA Operations runbook viewer (OPS-002). `$slug` is a
  // TanStack path param resolved by the canonical registry (app/runbooks.ts); no
  // navGroup so it never appears in the SideNav.
  {
    key: "runbook",
    path: "/operations/runbooks/$slug",
    titleKey: "route.runbook.title",
    subKey: "route.runbook.sub",
  },
];

/** Guaranteed fallback route (ROUTES is a non-empty literal). */
export const DEFAULT_ROUTE = ROUTES[0] as RouteDef;

export const NAV_GROUPS: readonly {
  id: "workspace" | "reference";
  labelKey: MessageKey;
}[] = [
  { id: "workspace", labelKey: "nav.group.workspace" },
  { id: "reference", labelKey: "nav.group.reference" },
];
