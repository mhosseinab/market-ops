import { createI18n } from "@market-ops/locale";
import { render, within } from "@testing-library/react";
import type { ReactNode } from "react";
import { I18nextProvider } from "react-i18next";
import { describe, expect, it } from "vitest";
import { LocaleContext } from "../app/i18n";
import type { ConnectorHealth } from "../data/connectorHealth";
import { ConnectorHealthPill } from "./ConnectorHealthPill";

// Pinned to English so the assertions can name the exact copy; the tone/label
// MAP under test is locale-independent (the fa-IR pack is covered by the pseudo
// suite in badges.pseudo.test.tsx).
const LOCALE = "en" as const;

// Issue #18: the connector pill must present every typed health with distinct
// copy + tone, and ONLY the confirmed-supported health may use the positive tone.
// Every non-positive health (unknown/disconnected/probing/degraded) fails closed.

function Harness({ children }: { children: ReactNode }) {
  const i18n = createI18n({ lng: LOCALE });
  return (
    <LocaleContext.Provider value={{ locale: LOCALE, setLocale: () => {} }}>
      <I18nextProvider i18n={i18n}>{children}</I18nextProvider>
    </LocaleContext.Provider>
  );
}

// Expected copy + tone per health. Only `supported` is positive (tone-pos).
const CASES: Array<{ health: ConnectorHealth; label: string; tone: string; positive: boolean }> = [
  { health: "unknown", label: "Connection status unknown", tone: "tone-muted", positive: false },
  { health: "disconnected", label: "Connection lost", tone: "tone-risk", positive: false },
  { health: "probing", label: "Verifying connection", tone: "tone-info", positive: false },
  { health: "degraded", label: "Connection unstable", tone: "tone-warn", positive: false },
  { health: "supported", label: "Connection healthy", tone: "tone-pos", positive: true },
];

describe("ConnectorHealthPill (issue #18)", () => {
  for (const c of CASES) {
    it(`renders distinct copy, tone, and accessible label for ${c.health}`, () => {
      const { container } = render(
        <Harness>
          <ConnectorHealthPill health={c.health} />
        </Harness>,
      );
      const pill = container.querySelector<HTMLElement>(".connection-pill");
      expect(pill).not.toBeNull();
      expect(pill?.textContent).toContain(c.label);
      expect(pill?.className).toContain(c.tone);
      expect(pill?.getAttribute("data-health")).toBe(c.health);
      // Accessible label names the current status.
      expect(pill?.getAttribute("aria-label")).toBe(`Connection status: ${c.label}`);
      // Fail closed: the positive tone appears ONLY for the supported health.
      expect(pill?.className.includes("tone-pos")).toBe(c.positive);
    });
  }

  it("never presents a non-supported health with the positive tone", () => {
    for (const c of CASES.filter((x) => !x.positive)) {
      const { container } = render(
        <Harness>
          <ConnectorHealthPill health={c.health} />
        </Harness>,
      );
      const pill = within(container).getByRole("status");
      expect(pill.className).not.toContain("tone-pos");
    }
  });
});
