import { useQueryClient } from "@tanstack/react-query";
import { Outlet, useRouter } from "@tanstack/react-router";
import { useEffect } from "react";
import { registerUnauthenticatedHandler } from "../app/authEvents";
import { ChatDock } from "../chat/components/ChatDock";
import { SideNav } from "./SideNav";
import { TopBar } from "./TopBar";

// App shell: full-viewport flex row. SideNav (rightmost in RTL), then the main
// column with the TopBar and the active route. Direction is driven onto <html>
// from the locale pack, so this layout mirrors automatically (logical CSS).
//
// The ChatDock is a persistent dock layer over EVERY area (CHAT-001) — not a
// seventh nav route. It toggles from the TopBar; when closed it renders nothing.
//
// The shell only mounts under the authenticated layout, so it is the natural seam
// to arm the session-expiry redirect (issue #168): when a protected query fails
// UNAUTHENTICATED (401), the global query error boundary calls the registered
// handler, which clears every protected/account-scoped query from the cache and
// routes to the login screen, preserving where the user was.
export function AppShell() {
  const router = useRouter();
  const queryClient = useQueryClient();

  useEffect(
    () =>
      registerUnauthenticatedHandler(() => {
        const destination = router.state.location.href;
        queryClient.clear();
        void router.navigate({ to: "/login", search: { redirect: destination } });
      }),
    [router, queryClient],
  );

  return (
    <div className="app-shell">
      <SideNav />
      <div className="main-col">
        <TopBar />
        <main className="route-scroll">
          <Outlet />
        </main>
      </div>
      <ChatDock />
    </div>
  );
}
