import { PSEUDO_CLOSE, PSEUDO_OPEN } from "@market-ops/locale";
import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { BulkToolbar } from "../components/BulkToolbar";
import { CoverageBars } from "../components/CoverageBars";
import { QueueCard } from "../components/QueueCard";
import { PseudoHarness } from "../test/pseudoHarness";

// LOC-011 pseudo pass for the NEW S28 copy-bearing components (BulkToolbar,
// CoverageBars, QueueCard). Under the pseudo pack every user-facing string must be
// bracketed (proves catalog resolution, not a hardcoded literal) and survive IN
// FULL (expansion pad + close bracket intact).
function assertPseudo(text: string) {
  expect(text.includes(PSEUDO_OPEN), `not bracketed: ${JSON.stringify(text)}`).toBe(true);
  expect(text).toContain(`·${PSEUDO_CLOSE}`);
}

describe("pseudo-localization for S28 components (LOC-011)", () => {
  it("bulk toolbar, coverage bars, and queue cards resolve copy via the catalog", () => {
    const { container } = render(
      <PseudoHarness>
        <BulkToolbar
          lineage="sel-1"
          version={2}
          previewedVersion={1}
          counts={{ executable: 1, warning: 0, blocked: 1 }}
          aggregateImpact={<span />}
          maxMovement={<span />}
          exclusions={<span />}
          onPreview={() => {}}
          onApprove={() => {}}
        />
        <CoverageBars
          segments={[
            { id: "fresh", labelKey: "freshness.fresh", tone: "pos", count: 1 },
            { id: "stale", labelKey: "freshness.stale", tone: "risk", count: 0 },
          ]}
        />
        <QueueCard
          titleKey="operations.queue.failedSync"
          descKey="operations.queue.failedSync.desc"
          accent="risk"
          count={<span />}
        />
      </PseudoHarness>,
    );

    for (const el of container.querySelectorAll(
      ".panel__title, .stat-card__label, .bulk-toolbar__aggregate dt, .approval-card__footnote, .banner__title, .coverage__label, .queue-card__title, .queue-card__desc",
    )) {
      assertPseudo(el.textContent ?? "");
    }
  });
});
