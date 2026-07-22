import { expect, test } from "@playwright/test";

// Journey (chat dock) — briefing → investigate → prepare → approve via card, and
// the headline containment proof: free text NEVER approves. Runs against the REAL
// core (seeded via `task db:reset`) with the gateway `/chat` SSE. The LLM plane is
// best-effort behind a kill switch (CHAT-009): when chat is unavailable the dock
// degrades to a read-only, screens-only fallback — the smoke asserts that path too
// rather than failing, exactly as the never-cut invariant requires.
//
// The two invariants asserted unconditionally:
//   • The dock is reachable in one interaction from an area (CHAT-001).
//   • Typing "approve it" mutates NOTHING — the composer owns no approval; the only
//     confirm path is the structured ApprovalCard control (§8, CHAT-041).

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

test("chat dock is reachable in one interaction and shows the containment footnote (CHAT-001)", async ({
  page,
}) => {
  await page.goto("/today");
  const toggle = page.getByTestId("chat-toggle");
  await toggle.click();
  const dock = page.getByTestId("chat-dock");
  await expect(dock).toBeVisible();
  await expect(page.getByTestId("chat-footnote")).toBeVisible();
});

test("free-text 'approve it' changes nothing; screens-only fallback stays functional (§8, CHAT-009)", async ({
  page,
}) => {
  await page.goto("/today");
  // CHAT-009's core claim is that a STRUCTURED, DATA-BEARING screen keeps
  // working while chat is unavailable — not merely that a generic wrapper div
  // is present (which is also true in the error state). Today performs a real
  // gateway fetch, so this is the genuine "still functioning" proof: a real
  // loaded/empty marker, and explicitly NOT the generic error wrapper.
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(
    page.getByTestId("today-no-action").or(page.getByTestId("today-queue")),
  ).toBeVisible();

  const toggle = page.getByTestId("chat-toggle");
  await toggle.click();
  await expect(page.getByTestId("chat-dock")).toBeVisible();

  const input = page.getByTestId("chat-input");
  await input.fill("approve it");
  await page.getByTestId("chat-send").click();

  // Whether chat is live or killed, no approval control is ever satisfied by the
  // typed text: the composer never renders an executable confirm from free text.
  // A confirm control only exists inside a structured ApprovalCard part.
  await page.waitForTimeout(1500);
  const composerConfirm = page
    .getByTestId("chat-input")
    .locator("xpath=following::*[@data-testid='confirm-approval']");
  await expect(composerConfirm).toHaveCount(0);

  // Screens-only fallback: the structured Actions surface remains fully usable
  // regardless of chat availability (CHAT-009). Bare `/actions` (no deep-linked
  // action id) renders ViewState's `isEmpty` branch WITHOUT issuing a fetch
  // (Actions.tsx: `isEmpty={!actionId || !exec}`) — so the Today assertion
  // above is this test's real network-backed CHAT-009 proof; this final check
  // still pins that /actions itself never falls into the generic error wrapper.
  await page.goto("/actions");
  await expect(page.locator(".screen").first()).toBeVisible();
  await expect(page.locator(".view-error")).toHaveCount(0);
});
