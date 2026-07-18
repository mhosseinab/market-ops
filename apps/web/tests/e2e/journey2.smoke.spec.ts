import { expect, test } from "@playwright/test";

// Journey 2 — daily decision on screens — smoke against the REAL core (seeded via
// `task db:reset`). It drives the ranked Today → event detail → recommendation →
// approval chain and verifies the never-cut UI safety behaviors:
//   • Screens-only fallback always works (CHAT-009): every surface renders its
//     structured state even when data is absent — never a crash.
//   • The approval control is a STRUCTURED button, never a free-text confirm
//     (§8 free-text containment); the "free text never executes" footnote is
//     present on the recommendation surface.
//   • When a live control is reachable, confirming lands in the recommend-only
//     terminal (Approved / awaiting external execution, EXE-005).
//
// The P0 gateway has no event→card linkage, so the deterministic version-binding
// and invalidation proofs live in the MSW component suite (Recommendation.test).
// This smoke asserts the reachable chain plus the structured fallback.

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

test("today → event → recommendation renders the structured approval surface", async ({ page }) => {
  await page.goto("/today");
  // Today renders a GENUINE loaded/empty state (never the generic error wrapper,
  // which the bare `.screen` root class alone cannot distinguish — a data-fetch
  // regression must fail this assertion, not silently degrade to a "passing"
  // error screen).
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(
    page.getByTestId("today-no-action").or(page.getByTestId("today-queue")),
  ).toBeVisible();

  // If a ranked, actionable event is present, follow it into the event detail.
  const review = page.getByTestId("event-review").first();
  if (await review.count()) {
    await review.click();
    const toRec = page.getByTestId("event-to-recommendation");
    await expect(toRec).toBeVisible();
    await toRec.click();
    await expect(page).toHaveURL(/recommendation/);
  } else {
    await page.goto("/recommendation");
  }

  // The recommendation surface always renders a structured, non-error state (an
  // approval card, or the no-card fallback) — screens-only fallback (CHAT-009).
  // `.screen-empty` here is Recommendation.tsx's own ternary INSIDE ViewState's
  // children slot, which only renders when ViewState's `error` prop is false —
  // so this pair is already error-exclusive; the explicit `.view-error` count
  // check pins that invariant rather than relying on it implicitly.
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("approval-card").or(page.locator(".screen-empty"))).toBeVisible();
});

test("the approval control is a structured button, never a free-text confirm", async ({ page }) => {
  await page.goto("/recommendation");
  await expect(page.locator(".view-error")).toHaveCount(0);

  const card = page.getByTestId("approval-card");
  if (await card.count()) {
    // The footnote makes the containment rule explicit on the surface.
    await expect(page.getByTestId("approval-footnote")).toBeVisible();

    const confirm = page.getByTestId("confirm-approval");
    await expect(confirm).toHaveJSProperty("tagName", "BUTTON");

    if (await confirm.isEnabled()) {
      await confirm.click();
      // Recommend-only terminal: Approved / awaiting external execution (EXE-005).
      await expect(page.getByTestId("recommend-only")).toBeVisible();
    }
  } else {
    // No live card reachable from this surface; the no-card fallback still
    // renders (NOT the generic error wrapper — asserted above).
    await expect(page.locator(".screen-empty")).toBeVisible();
  }
});
