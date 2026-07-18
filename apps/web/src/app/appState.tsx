import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from "react";

// Global UI state (design/README.md §"State Management"): theme, density, and the
// chat-dock toggle. Theme/density are applied as data-attributes on <html> so the
// token set swaps with no per-component branch.

export type Theme = "light" | "dark";
export type Density = "comfortable" | "dense";

interface AppState {
  theme: Theme;
  density: Density;
  chatOpen: boolean;
  briefingUnseen: boolean;
  toggleTheme: () => void;
  toggleDensity: () => void;
  toggleChat: () => void;
}

const AppStateContext = createContext<AppState | null>(null);

export function AppStateProvider({ children }: { children: ReactNode }) {
  const [theme, setTheme] = useState<Theme>("light");
  const [density, setDensity] = useState<Density>("comfortable");
  const [chatOpen, setChatOpen] = useState(false);
  const [briefingUnseen, setBriefingUnseen] = useState(true);

  useEffect(() => {
    document.documentElement.setAttribute("data-theme", theme);
    document.documentElement.setAttribute("data-density", density);
  }, [theme, density]);

  const value = useMemo<AppState>(
    () => ({
      theme,
      density,
      chatOpen,
      briefingUnseen,
      toggleTheme: () => setTheme((t) => (t === "light" ? "dark" : "light")),
      toggleDensity: () => setDensity((d) => (d === "comfortable" ? "dense" : "comfortable")),
      toggleChat: () => {
        setChatOpen((o) => !o);
        setBriefingUnseen(false);
      },
    }),
    [theme, density, chatOpen, briefingUnseen],
  );

  return <AppStateContext.Provider value={value}>{children}</AppStateContext.Provider>;
}

export function useAppState(): AppState {
  const ctx = useContext(AppStateContext);
  if (!ctx) throw new Error("useAppState must be used within AppStateProvider");
  return ctx;
}
