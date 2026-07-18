import { expect, test } from "@playwright/test";

// Journey 3 — bulk approval on screens — smoke against the REAL core (seeded via
// `task db:reset`). It drives the never-cut bulk safety behavior end-to-end on the
// Bulk screen:
//   • The selection set is VERSIONED; a preview binds the structured control to an
//     exact version (APR-001 at the set level).
//   • ANY change to the set (a filter toggle) mints a new version and INVALIDATES
//     the preview — the approve control disables behind a re-preview requirement.
//   • A fresh preview re-binds; only then can the structured control confirm, and
//     the confirm is a BUTTON bound to the version — free text never confirms.
//
// The P0 gateway has no server-side selection-set/preview endpoint, so the final
// confirm may be refused for a client-synthesized lineage; the version-binding and
// per-item MSW proofs live in BulkApproval.test. This smoke asserts the reachable
// structured invalidation chain plus containment.

const GATEWAY = process.env.VITE_GATEWAY_BASE_URL ?? "http://localhost:8080";

test.beforeEach(async ({ context }) => {
  const email = process.env.E2E_EMAIL;
  const password = process.env.E2E_PASSWORD;
  if (!email || !password) return;
  const res = await context.request.post(`${GATEWAY}/auth/login`, {
    data: { email, password },
  });
  expect(res.ok(), "seeded login should succeed").toBeTruthy();
});

test("bulk: preview → mutate set → invalidated → re-preview → structured confirm", async ({
  page,
}) => {
  await page.goto("/bulk");
  await expect(page.locator(".screen")).toBeVisible();

  const preview = page.getByTestId("bulk-preview");
  if (!(await preview.count())) {
    // No candidates reachable from this surface; the structured EMPTY state
    // renders. `.view-error` is deliberately EXCLUDED from this assertion — an
    // error state is a real failure, not a legitimate "no candidates" outcome,
    // and must not be able to satisfy this branch (a data-fetch regression must
    // fail this test, not silently pass as "the empty state rendered").
    await expect(page.locator(".view-error")).toHaveCount(0);
    await expect(page.locator(".screen-empty, .view-loading")).toBeVisible();
    return;
  }

  // Containment is explicit on the surface, and the confirm is a structured button.
  await expect(page.getByTestId("bulk-footnote")).toBeVisible();
  const approve = page.getByTestId("bulk-approve");
  await expect(approve).toHaveJSProperty("tagName", "BUTTON");

  // Preview binds the control to the current selection-set version.
  await preview.click();

  // Mutating the set (a filter toggle) mints a new version → preview INVALIDATED.
  const chip = page.locator(".filter-chip").nth(1);
  if (await chip.count()) {
    await chip.click();
    await expect(page.getByTestId("bulk-invalidated")).toBeVisible();
    // The approve control is disabled while the bound version is stale.
    await expect(approve).toBeDisabled();

    // A fresh preview clears the invalidation.
    await preview.click();
    await expect(page.getByTestId("bulk-invalidated")).toHaveCount(0);
  }

  // Only if a live executable candidate is present does the control enable; a valid
  // bulk confirmation lands recommend-only (EXE-005). Guarded — the seed may hold
  // no executable candidate, and a synthetic lineage may be refused server-side.
  if (await approve.isEnabled()) {
    await approve.click();
    const recommendOnly = page.getByTestId("bulk-recommend-only");
    const staleResult = page.getByTestId("bulk-stale-result");
    await expect(recommendOnly.or(staleResult)).toBeVisible();
  }
});
