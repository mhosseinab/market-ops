import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import type { ReactElement } from "react";
import { AppShell } from "../components/AppShell";
import { EmptyState } from "../components/EmptyState";
import { Actions } from "../screens/Actions";
import { BulkApproval } from "../screens/BulkApproval";
import { CostImport } from "../screens/CostImport";
import { Diagnostics } from "../screens/Diagnostics";
import { EventDetail } from "../screens/EventDetail";
import { Market } from "../screens/Market";
import { Onboarding } from "../screens/Onboarding";
import { Operations } from "../screens/Operations";
import { ProductDetail } from "../screens/ProductDetail";
import { Products } from "../screens/Products";
import { Recommendation } from "../screens/Recommendation";
import { RunbookViewer } from "../screens/RunbookViewer";
import { Settings } from "../screens/Settings";
import { Today } from "../screens/Today";
import { ROUTES, type RouteKey } from "./navConfig";

// Code-based route tree derived from the DATA in navConfig. Root renders the
// AppShell; `/` deep-links to Today; every screen + deep-link sub-route is a
// leaf. S26 mounts the real onboarding/products/product/cost screens; the
// remaining screens stay EmptyState scaffolds until S27–S28. Every route accepts
// an optional `variantId` search key so deep links (products → product,
// product → cost/diagnostics) stay typed end-to-end.

const rootRoute = createRootRoute({ component: AppShell });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    // Path validation needs the registered router, which is built from this tree
    // (circular at definition time); the target is a real registered route.
    throw redirect({ to: "/today" } as never);
  },
});

const SCREENS: Partial<Record<RouteKey, () => ReactElement>> = {
  today: Today,
  products: Products,
  product: ProductDetail,
  diagnostics: Diagnostics,
  cost: CostImport,
  onboarding: Onboarding,
  event: EventDetail,
  recommendation: Recommendation,
  market: Market,
  actions: Actions,
  bulk: BulkApproval,
  settings: Settings,
  operations: Operations,
  runbook: RunbookViewer,
};

/** Uniform, permissive search validation so typed deep-link keys stay typed. */
function validateSearch(search: Record<string, unknown>): {
  variantId?: string;
  eventId?: string;
  cardId?: string;
  recommendationId?: string;
  actionId?: string;
} {
  const out: {
    variantId?: string;
    eventId?: string;
    cardId?: string;
    recommendationId?: string;
    actionId?: string;
  } = {};
  if (typeof search.variantId === "string") out.variantId = search.variantId;
  if (typeof search.eventId === "string") out.eventId = search.eventId;
  if (typeof search.cardId === "string") out.cardId = search.cardId;
  if (typeof search.recommendationId === "string") out.recommendationId = search.recommendationId;
  if (typeof search.actionId === "string") out.actionId = search.actionId;
  return out;
}

const screenRoutes = ROUTES.map((r) =>
  createRoute({
    getParentRoute: () => rootRoute,
    path: r.path,
    component: SCREENS[r.key] ?? EmptyState,
    validateSearch,
  }),
);

const routeTree = rootRoute.addChildren([indexRoute, ...screenRoutes]);

export function createAppRouter(history?: RouterHistory) {
  return createRouter({ routeTree, ...(history ? { history } : {}) });
}

export const router = createAppRouter();

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
