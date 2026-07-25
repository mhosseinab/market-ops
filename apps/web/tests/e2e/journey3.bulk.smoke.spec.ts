import { expect, test } from "@playwright/test";
import {
  expectNoRequiredOpFailures,
  guardRequiredOps,
  loginAsSeededOwner,
  OFFER_C_IDENTITY,
} from "./fixtures";

// Journey 3 — bulk approval on screens — a NON-VACUOUS smoke against the REAL
// core (seeded via `task db:reset`, services/core/fixtures/dev_seed.sql). It
// drives the never-cut bulk safety behavior end to end, and every assertion is
// UNCONDITIONAL (issue #84 / the S32 duplicate-root expansion):
//   • The selection set is versioned BY THE SERVER; a preview binds the
//     structured control to an exact (lineage, version) pair — APR-001 at the
//     set level.
//   • ANY change to the set (a filter toggle) mints a new local revision and
//     INVALIDATES the preview: the approve control disables behind a
//     re-preview requirement.
//   • A fresh preview re-binds; only then may the structured control confirm,
//     and the per-item outcome rendered afterwards is the SERVER's.
//
// WHAT THIS GATE USED TO DO, AND WHY IT WAS VACUOUS: it returned SUCCESSFULLY
// when `bulk-preview` was absent, and wrapped the entire invalidation proof in
// `if (await chip.count())` and the confirmation in `if (await
// approve.isEnabled())`. With no seeded candidates it took the early return on
// the very first check, so the invalidation and confirmation branches it claims
// to verify were never entered.
//
// WHY IT NOW FAILS WHEN THE BEHAVIOR IS ABSENT:
//   • Remove the seeded observation targets/offers ⇒ ViewState renders its
//     empty branch, `bulk-toolbar` never appears ⇒ FAIL, no early return.
//   • Remove the seeded CARD for variant C ⇒ the actions queue yields no
//     (variantId, recommendationId) pair, so `bulk-preview-empty` renders,
//     `counts.executable` stays 0 and the approve control NEVER enables ⇒ FAIL.
//   • Break invalidation (a set mutation that does not invalidate the preview)
//     ⇒ `bulk-invalidated` never renders and approve stays enabled ⇒ FAIL.
//   • A 401/403/500 on any required op trips the guard.

const REQUIRED_OP = [
  "/auth/login",
  "/auth/me",
  "/observation/targets",
  "/observation/observed-offers",
  "/cost/readiness",
  "/actions",
  "/selection-sets/preview",
  "/approvals/bulk/confirm",
];

test.beforeEach(async ({ context }) => {
  await loginAsSeededOwner(context);
});

test("journey 3: server-minted preview → set mutation invalidates → re-preview → structured bulk confirm", async ({
  page,
}) => {
  const failures = guardRequiredOps(page, REQUIRED_OP);

  await page.goto("/bulk");

  // A GENUINE loaded state with real candidates — never the error wrapper, and
  // never the "no candidates" empty branch (which the old spec accepted as a
  // pass). The toolbar renders only INSIDE ViewState's loaded children.
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("bulk-toolbar")).toBeVisible();

  // Contract-backed candidate ROWS from /observation/targets + observed offers.
  const rows = page.locator(".data-table__row");
  await expect(rows.first()).toBeVisible();

  // The executable candidate (variant C) is present as a real row, keyed by its
  // per-offer include control — proof the readiness + actions seams resolved,
  // not merely that a table drew. Assertions below are scoped to THIS row, so
  // another candidate can never stand in for it (a whole-screen "something got
  // authorized" assertion passes even when C is missing — that is precisely the
  // vacuity class this issue is about).
  const rowC = page.locator(".data-table__row", {
    has: page.getByTestId(`bulk-include-${OFFER_C_IDENTITY}`),
  });
  await expect(rowC).toHaveCount(1);
  await expect(rowC.getByTestId(`bulk-include-${OFFER_C_IDENTITY}`)).toBeChecked();

  // Containment is explicit and the confirm is a structured BUTTON.
  await expect(page.getByTestId("bulk-footnote")).toBeVisible();
  const approve = page.getByTestId("bulk-approve");
  await expect(approve).toHaveJSProperty("tagName", "BUTTON");

  // Before any preview there is nothing to bind to: the control is inert and the
  // surface says so.
  await expect(page.getByTestId("preview-required")).toBeVisible();
  await expect(approve).toBeDisabled();

  // ── Preview: the SERVER mints the selection set and its version. ───────────
  const preview = page.getByTestId("bulk-preview");
  await preview.click();

  // A real server-minted version is now bound to the control.
  const selectionSet = page.getByTestId("selection-set");
  await expect(selectionSet).toHaveAttribute("data-version", "1");
  await expect(page.getByTestId("bulk-toolbar")).toHaveAttribute("data-preview-valid", "true");
  await expect(approve).toBeEnabled();

  // ── Mutate the set: a filter toggle mints a new local revision, so the
  // previously bound server version no longer describes the selection. ───────
  await page.locator(".filter-chip").nth(1).click();
  await expect(page.getByTestId("bulk-invalidated")).toBeVisible();
  await expect(page.getByTestId("bulk-toolbar")).toHaveAttribute("data-preview-valid", "false");
  await expect(approve).toBeDisabled();

  // ── Re-preview: the server mints the NEXT version in the same lineage and the
  // control re-binds. The version increments server-side — the browser never
  // counts versions itself. ─────────────────────────────────────────────────
  await preview.click();
  await expect(page.getByTestId("bulk-invalidated")).toHaveCount(0);
  await expect(selectionSet).toHaveAttribute("data-version", "2");
  await expect(approve).toBeEnabled();

  // ── Confirm through the structured control, bound to that exact version. ──
  await approve.click();

  // The aggregate recommend-only terminal (EXE-005), and — bound to the SPECIFIC
  // seeded candidate — the SERVER's authoritative per-item outcome for variant C.
  // Scoping to `rowC` is what makes this non-vacuous: a screen-wide
  // "some row was authorized" check still passes when C's own card is gone,
  // because another candidate satisfies it. Here, C's card missing ⇒ C is not a
  // selection member ⇒ its cell renders `result-excluded` ⇒ this FAILS.
  await expect(page.getByTestId("bulk-recommend-only")).toBeVisible();
  await expect(rowC.getByTestId("result-authorized")).toBeVisible();

  expectNoRequiredOpFailures(failures);
});
