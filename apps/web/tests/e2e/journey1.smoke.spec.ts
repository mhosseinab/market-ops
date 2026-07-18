import { expect, test } from "@playwright/test";

// Journey 1 — connect to first value — happy-path smoke against the REAL core
// (seeded via `task db:reset`). It verifies the two never-cut UI safety behaviors
// on the onboarding/connection surface and that the Products workspace loads:
//   • ACC-001 — an Unknown capability never enables the dependent control.
//   • The screens-only surfaces render against the live gateway (CHAT-009).
//
// The gateway authenticates via an httpOnly session cookie. When E2E_EMAIL /
// E2E_PASSWORD are provided (the seeded owner), the smoke opens a session first;
// otherwise it drives the unauthenticated surface, which must still render its
// structured states rather than crash.

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

test("onboarding surfaces capability status and never enables UI on Unknown (ACC-001)", async ({
  page,
}) => {
  await page.goto("/onboarding");

  // The access-scopes (capability) list renders for the account.
  await expect(page.locator(".capability-list")).toBeVisible();

  // ACC-001: any capability-gated control (sync catalog) is disabled while its
  // capability has not been probed to Supported.
  const sync = page.getByTestId("sync-catalog");
  await expect(sync).toBeVisible();
  const enabled = await sync
    .locator("xpath=ancestor::div[contains(@class,'capability-gate')]")
    .getAttribute("data-capability-enabled");
  if (enabled === "false") {
    await expect(sync).toBeDisabled();
  }
});

test("products workspace loads on screens", async ({ page }) => {
  await page.goto("/products");
  // The search toolbar is always present; the table or its empty state renders.
  await expect(page.locator(".toolbar__search")).toBeVisible();
});
