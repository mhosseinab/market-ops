import { PSEUDO_CLOSE, PSEUDO_OPEN } from "@market-ops/locale";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AvailabilityBadge, CapabilityBadge, DispositionBadge } from "../components/badges";
import { CapabilityGate } from "../components/CapabilityGate";
import { Section } from "../components/primitives";
import { Stepper } from "../components/Stepper";
import { PseudoHarness } from "../test/pseudoHarness";

// LOC-011 pseudo pass for the NEW S26 copy-bearing components: under the pseudo
// pack every user-facing string must be bracketed (proves it resolved through the
// catalog, not a hardcoded literal) and appear IN FULL (the expansion pad + close
// bracket survive — nothing clipped).
function assertPseudo(text: string) {
  expect(text.includes(PSEUDO_OPEN), `not bracketed: ${JSON.stringify(text)}`).toBe(true);
  expect(text).toContain(`·${PSEUDO_CLOSE}`);
}

describe("pseudo-localization for S26 components (LOC-011)", () => {
  it("badges, section titles, stepper labels, and the gated note resolve via the catalog", () => {
    const { container } = render(
      <PseudoHarness>
        <CapabilityBadge state="unknown" />
        <AvailabilityBadge state="in_stock" />
        <DispositionBadge state="duplicate" />
        <Section titleKey="cost.preview.title">
          <span />
        </Section>
        <Stepper steps={[{ id: "s", labelKey: "onboarding.step.connectDk", state: "active" }]} />
        <CapabilityGate state="unknown">{() => <button type="button">·</button>}</CapabilityGate>
      </PseudoHarness>,
    );

    for (const el of container.querySelectorAll(
      ".badge, .panel__title, .stepper__label, .capability-gate__note",
    )) {
      assertPseudo(el.textContent ?? "");
    }
  });
});
