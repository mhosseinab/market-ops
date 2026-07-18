import { Link } from "@tanstack/react-router";
import type { ReactNode } from "react";

// Thin navigation wrapper. The S25 router is DATA-driven (routes built by mapping
// navConfig), so TanStack cannot derive a typed `to` union from the tree — the
// same reason SideNav passes a plain string path. AppLink keeps deep links in one
// place and isolates that single `never` cast rather than scattering it. `search`
// is the typed deep-link payload (currently the optional `variantId`).
export function AppLink({
  to,
  search,
  className,
  children,
  testId,
}: {
  to: string;
  search?: { variantId?: string; eventId?: string; cardId?: string; actionId?: string };
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
