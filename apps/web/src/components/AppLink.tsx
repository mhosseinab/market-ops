import { Link, useNavigate } from "@tanstack/react-router";
import type { ReactNode } from "react";

// Thin navigation wrapper. The S25 router is DATA-driven (routes built by mapping
// navConfig), so TanStack cannot derive a typed `to` union from the tree — the
// same reason SideNav passes a plain string path. AppLink keeps deep links in one
// place and isolates that single `never` cast rather than scattering it. `search`
// is the typed deep-link payload (currently the optional `variantId`).

/** The typed deep-link payload every route's validateSearch accepts. */
export interface DeepLinkSearch {
  variantId?: string;
  eventId?: string;
  cardId?: string;
  actionId?: string;
  recommendationId?: string;
}

/**
 * PROGRAMMATIC deep-link navigation, for state a screen must keep in the URL
 * (e.g. the Actions queue's selected card, so a selection survives a reload and
 * is shareable). It is the imperative twin of AppLink and exists for the same
 * reason: to hold the data-driven-router `never` cast in ONE place.
 */
export function useDeepLinkNavigate(): (to: string, search?: DeepLinkSearch) => void {
  const navigate = useNavigate();
  return (to, search) => {
    void navigate({ to: to as never, search: (search ?? {}) as never });
  };
}

export function AppLink({
  to,
  search,
  className,
  children,
  testId,
}: {
  to: string;
  search?: DeepLinkSearch;
  className?: string;
  children: ReactNode;
  testId?: string;
}) {
  return (
    <Link
      to={to as never}
      search={(search ?? {}) as never}
      className={className}
      data-testid={testId}
    >
      {children}
    </Link>
  );
}
