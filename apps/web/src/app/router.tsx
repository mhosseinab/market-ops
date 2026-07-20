import type { QueryClient } from "@tanstack/react-query";
import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import type { ReactElement } from "react";
import { AppShell } from "../components/AppShell";
import { EmptyState } from "../components/EmptyState";
import { sessionQueryOptions } from "../data/hooks";
import { Actions } from "../screens/Actions";
import { BulkApproval } from "../screens/BulkApproval";
import { CostImport } from "../screens/CostImport";
import { Diagnostics } from "../screens/Diagnostics";
import { EventDetail } from "../screens/EventDetail";
import { AuthGateLoading, Login } from "../screens/Login";
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
import { queryClient as defaultQueryClient } from "./query";

// Code-based route tree derived from the DATA in navConfig. The tree splits into
// a PUBLIC `/login` route and an AUTHENTICATED layout: the authed layout renders
// the AppShell and, in its `beforeLoad`, resolves GET /auth/me BEFORE any child
// screen (and thus any protected/account-scoped query) mounts — the fail-closed
// auth gate (issue #168). An unresolved session redirects to `/login` preserving
// the intended destination. Every screen + deep-link sub-route is a leaf under the
// authed layout; each accepts optional typed search keys so deep links stay typed.

const rootRoute = createRootRouteWithContext<{ queryClient: QueryClient }>()();

// PUBLIC: the login screen. It carries no AppShell chrome and no auth gate, and
// preserves the destination the user was headed to via the `redirect` search key.
const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: Login,
  validateSearch: (search: Record<string, unknown>): { redirect?: string } =>
    typeof search.redirect === "string" ? { redirect: search.redirect } : {},
});

// AUTHENTICATED layout (pathless): the gate. `beforeLoad` blocks the route load —
// so no child screen mounts and no protected query fires — until the session is
// confirmed. A failure (absent/expired session, or any /auth/me error) redirects
// to `/login`; the shared session cache entry means a later screen reads the
// already-resolved principal without a second round trip.
const authedRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "authed",
  component: AppShell,
  pendingComponent: AuthGateLoading,
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.ensureQueryData(sessionQueryOptions());
    } catch {
      // Fail closed: no confirmed session ⇒ no protected screen. Preserve where the
      // user was going so login can return them there.
      throw redirect({ to: "/login", search: { redirect: location.href } });
    }
  },
});

const indexRoute = createRoute({
  getParentRoute: () => authedRoute,
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
    getParentRoute: () => authedRoute,
    path: r.path,
    component: SCREENS[r.key] ?? EmptyState,
    validateSearch,
  }),
);

const routeTree = rootRoute.addChildren([
  loginRoute,
  authedRoute.addChildren([indexRoute, ...screenRoutes]),
]);

export function createAppRouter(
  queryClient: QueryClient = defaultQueryClient,
  history?: RouterHistory,
) {
  return createRouter({ routeTree, context: { queryClient }, ...(history ? { history } : {}) });
}

export const router = createAppRouter();

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
