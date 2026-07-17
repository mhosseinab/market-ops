import { PSEUDO_CLOSE, PSEUDO_OPEN } from "@market-ops/locale";
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { PseudoHarness } from "../test/pseudoHarness";
import { EventTypeBadge, FreshnessPill, QualityBadge, ReadinessBadge, StatusBadge } from "./badges";
import { EmptyState } from "./EmptyState";

// LOC-011 pseudo-localization suite. Under the pseudo pack every user-facing
// string must be bracketed (proves it resolved through the catalog — untranslated
// or hardcoded copy would not be) and must appear IN FULL (a clipped/truncated
// render would drop the trailing sentinel + close bracket).

function assertPseudo(text: string) {
  expect(text.includes(PSEUDO_OPEN), `not bracketed: ${JSON.stringify(text)}`).toBe(true);
  expect(text.includes(PSEUDO_CLOSE), `clipped/no close: ${JSON.stringify(text)}`).toBe(true);
  // The expansion pad (·) sits just before the close bracket; its presence proves
  // the string was not truncated.
  expect(text).toContain(`·${PSEUDO_CLOSE}`);
}

describe("pseudo-localization renders (LOC-011)", () => {
  it("badges resolve every label through the catalog", () => {
    const { container } = render(
      <PseudoHarness>
        <QualityBadge state="verified" />
        <QualityBadge state="conflicted" />
        <ReadinessBadge state="missing" />
        <StatusBadge state="accepted" />
        <StatusBadge state="pendingReconciliation" />
        <EventTypeBadge type={1} />
        <FreshnessPill ageMinutes={30} />
        <EmptyState />
      </PseudoHarness>,
    );
    for (const el of container.querySelectorAll(".badge, .screen-empty p")) {
      assertPseudo(el.textContent ?? "");
    }
  });

  it("keeps the parameterized marketplace slot inside the pseudo string", () => {
    render(
      <PseudoHarness>
        <StatusBadge state="accepted" />
      </PseudoHarness>,
    );
    // "Accepted by {marketplace}" → marketplace resolves too, still bracketed.
    const badge = screen.getByText(new RegExp(PSEUDO_OPEN));
    assertPseudo(badge.textContent ?? "");
  });
});
