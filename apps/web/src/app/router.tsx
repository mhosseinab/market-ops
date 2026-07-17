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
import { CostImport } from "../screens/CostImport";
import { Onboarding } from "../screens/Onboarding";
import { ProductDetail } from "../screens/ProductDetail";
import { Products } from "../screens/Products";
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
  products: Products,
  product: ProductDetail,
  cost: CostImport,
  onboarding: Onboarding,
};

/** Uniform, permissive search validation so deep-link `variantId` stays typed. */
function validateSearch(search: Record<string, unknown>): { variantId?: string } {
  return typeof search.variantId === "string" ? { variantId: search.variantId } : {};
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
