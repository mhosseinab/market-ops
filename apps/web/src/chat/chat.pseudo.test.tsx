import { PSEUDO_CLOSE, PSEUDO_OPEN } from "@market-ops/locale";
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { PseudoHarness } from "../test/pseudoHarness";
import { EvidenceRefs } from "./components/EvidenceRefs";
import { InlineTableView } from "./components/InlineTableView";
import { Level2Card } from "./components/Level2Card";
import { PickerCard } from "./components/PickerCard";
import { StatementSection } from "./components/StatementSection";
import { STATEMENT_KINDS } from "./types";

// The chat cards render deep links (TanStack `Link`), which need a router context.
// Wrap the pseudo tree in a minimal memory router so the components mount fully.
function renderWithRouter(ui: ReactNode) {
  const rootRoute = createRootRoute({
    component: () => <PseudoHarness>{ui}</PseudoHarness>,
  });
  const router = createRouter({
    routeTree: rootRoute,
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  return render(<RouterProvider router={router as never} />);
}

// LOC-011 pseudo pass for the NEW S29 copy-bearing chat components that resolve
// copy through `useT` (statement labels, evidence, table summary, picker, L2).
// Under the pseudo pack every user-facing string must be bracketed (catalog
// resolution, not a hardcoded literal) and survive IN FULL. Grounded DATA (the
// assistant's lines, table cells, SKUs) is intentionally NOT bracketed.
function assertPseudo(text: string) {
  expect(text.includes(PSEUDO_OPEN), `not bracketed: ${JSON.stringify(text)}`).toBe(true);
  expect(text).toContain(`·${PSEUDO_CLOSE}`);
}

describe("pseudo-localization for S29 chat components (LOC-011)", () => {
  it("statement labels, evidence, table, picker, and L2 resolve via the catalog", () => {
    const { container } = renderWithRouter(
      <>
        {STATEMENT_KINDS.map((kind) => (
          <StatementSection key={kind} kind={kind} lines={["data"]} />
        ))}
        <EvidenceRefs evidence={[]} />
        <EvidenceRefs
          evidence={[{ ref: "obs-1", quality: "verified", capturedAt: "2026-07-17T09:00:00Z" }]}
        />
        <InlineTableView
          table={{
            headers: ["h"],
            rows: [["a"]],
            totalRows: 45,
            deepLink: { to: "/products" },
          }}
        />
        <PickerCard options={[{ id: "o1", label: "Sony", deepLink: { to: "/event" } }]} />
        <Level2Card proposal={{ before: "10:00", after: "12:00" }} />
      </>,
    );

    for (const el of container.querySelectorAll(
      ".statement__title, .statement__note, .chat-evidence__title, .chat-evidence__missing, .chat-table__summary, .chat-card__title, .chat-card__hint, .chat-l2__kv dt",
    )) {
      assertPseudo(el.textContent ?? "");
    }
  });
});
