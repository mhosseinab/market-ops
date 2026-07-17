import {
  createRootRoute,
  createRoute,
  createRouter,
  type RouterHistory,
  redirect,
} from "@tanstack/react-router";
import { AppShell } from "../components/AppShell";
import { EmptyState } from "../components/EmptyState";
import { ROUTES } from "./navConfig";

// Code-based route tree derived from the DATA in navConfig. Root renders the
// AppShell; `/` deep-links to Today; every screen + deep-link sub-route
// (event/recommendation/product/cost/bulk/diagnostics/onboarding/ds) is a leaf.
// Screen bodies are scaffold EmptyStates in S25; real screens land in S26–S28.

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

const screenRoutes = ROUTES.map((r) =>
  createRoute({ getParentRoute: () => rootRoute, path: r.path, component: EmptyState }),
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
