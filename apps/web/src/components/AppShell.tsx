import { Outlet } from "@tanstack/react-router";
import { ChatDock } from "../chat/components/ChatDock";
import { SideNav } from "./SideNav";
import { TopBar } from "./TopBar";

// App shell: full-viewport flex row. SideNav (rightmost in RTL), then the main
// column with the TopBar and the active route. Direction is driven onto <html>
// from the locale pack, so this layout mirrors automatically (logical CSS).
//
// The ChatDock is a persistent dock layer over EVERY area (CHAT-001) — not a
// seventh nav route. It toggles from the TopBar; when closed it renders nothing.
export function AppShell() {
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
