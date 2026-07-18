import { PSEUDO_CLOSE, PSEUDO_OPEN } from "@market-ops/locale";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { EvidencePanel } from "../components/EvidencePanel";
import { StateMachineView } from "../components/StateMachineView";
import { PseudoHarness } from "../test/pseudoHarness";

// LOC-011 pseudo pass for the NEW S27 copy-bearing components that resolve copy
// through `useT` only (StateMachineView, EvidencePanel). Under the pseudo pack
// every user-facing string must be bracketed (proves catalog resolution, not a
// hardcoded literal) and survive IN FULL (expansion pad + close bracket intact).
function assertPseudo(text: string) {
  expect(text.includes(PSEUDO_OPEN), `not bracketed: ${JSON.stringify(text)}`).toBe(true);
  expect(text).toContain(`·${PSEUDO_CLOSE}`);
}

describe("pseudo-localization for S27 components (LOC-011)", () => {
  it("state-machine stages and evidence panels resolve via the catalog", () => {
    const { container } = render(
      <PseudoHarness>
        <StateMachineView state="revalidating" />
        <StateMachineView state="invalidated" reason="parameter_version_changed" />
        <StateMachineView state="expired" />
        <StateMachineView state="approved" executionPending />
        <StateMachineView state="awaiting_confirmation" permissionDenied />
        <EvidencePanel kind="observed">
          <span />
        </EvidencePanel>
        <EvidencePanel kind="inference">
          <span />
        </EvidencePanel>
      </PseudoHarness>,
    );

    for (const el of container.querySelectorAll(
      ".panel__title, .sm-gates__item, .banner__title, .evidence-panel__title, .evidence-panel__note",
    )) {
      assertPseudo(el.textContent ?? "");
    }
  });
});
