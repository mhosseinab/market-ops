import { expect, type Page, test } from "@playwright/test";
import {
  expectNoRequiredOpFailures,
  guardRequiredOps,
  loginAsSeededOwner,
  VARIANT_B,
} from "./fixtures";

// Journey 4 — chat dock containment + the BLOCKER workflow on screens — a
// NON-VACUOUS smoke against the REAL core (seeded via `task db:reset`,
// services/core/fixtures/dev_seed.sql). In the S32 kill-switch run
// (tools/integration/run_killswitch_journey.sh) the LLM container is STOPPED
// before this executes, so it doubles as the screens-only fallback proof
// (CHAT-009, never-cut): losing the chat plane degrades ONLY chat.
//
// WHAT THIS GATE USED TO DO, AND WHY IT WAS VACUOUS: with a seed carrying only
// org/user/account rows, its CHAT-009 "screens still work" proof was satisfied
// by Today's `today-no-action` EMPTY state, and its containment proof was the
// mere ABSENCE of a confirm control near the composer — which is trivially true
// on a page that has no approval control anywhere. It never entered the blocker
// workflow it claims to exercise.
//
// WHY IT NOW FAILS WHEN THE BEHAVIOR IS ABSENT:
//   • Remove the seeded events ⇒ `today-queue` never renders ⇒ FAIL. The
//     screens-only proof is now a DATA-BEARING screen, not an empty shell.
//   • Remove the seeded BLOCKER event (variant B, unverified) ⇒ `event-blocked`
//     never renders and the blocker deep-link is unreachable ⇒ FAIL.
//   • Remove variant B's blocked readiness ⇒ the product detail's risk banner
//     (server-derived `missing`) never renders ⇒ FAIL.
//   • Containment is now asserted POSITIVELY: zero approval/execution requests
//     are issued while free text is typed and sent — not merely "no button
//     happened to be nearby".

const REQUIRED_OP = ["/auth/login", "/auth/me", "/today", "/catalog/product", "/cost/readiness"];

// The mutation endpoints free text may NEVER reach (§8, CHAT-041). Only a
// structured control bound to action id + versions can approve or execute.
const APPROVAL_MUTATION_PATH = ["/approvals/confirm", "/approvals/bulk/confirm", "/actions/retry"];

/** Record every approval/execution request the page issues, for containment. */
function recordApprovalMutations(page: Page): string[] {
  const calls: string[] = [];
  page.on("request", (req) => {
    const url = req.url();
    if (req.method() === "GET") return;
    if (APPROVAL_MUTATION_PATH.some((p) => url.includes(p))) {
      calls.push(`${req.method()} ${url}`);
    }
  });
  return calls;
}

test.beforeEach(async ({ context }) => {
  await loginAsSeededOwner(context);
});

test("journey 4: the chat dock is reachable in one interaction from an area (CHAT-001)", async ({
  page,
}) => {
  const failures = guardRequiredOps(page, REQUIRED_OP);

  await page.goto("/today");
  // The area behind the dock is a real, data-bearing screen.
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("today-queue")).toBeVisible();

  // ONE interaction opens the dock (it is a persistent dock on the area, never a
  // seventh product area).
  await page.getByTestId("chat-toggle").click();
  await expect(page.getByTestId("chat-dock")).toBeVisible();
  await expect(page.getByTestId("chat-footnote")).toBeVisible();

  expectNoRequiredOpFailures(failures);
});

test("journey 4: free-text 'approve it' issues NO approval request; screens stay fully functional (§8, CHAT-009)", async ({
  page,
}) => {
  const failures = guardRequiredOps(page, REQUIRED_OP);
  const approvalCalls = recordApprovalMutations(page);

  await page.goto("/today");

  // CHAT-009's real claim: a STRUCTURED, DATA-BEARING screen keeps working while
  // the chat plane is unavailable. The seeded ranked queue is that proof — an
  // empty shell can no longer satisfy it.
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("today-queue")).toBeVisible();
  await expect(page.getByTestId("event-row")).toHaveCount(2);

  await page.getByTestId("chat-toggle").click();
  await expect(page.getByTestId("chat-dock")).toBeVisible();

  await page.getByTestId("chat-input").fill("approve it");
  await page.getByTestId("chat-send").click();

  // Give any (mis)handled free-text approval a real chance to fire.
  await page.waitForTimeout(1500);

  // Containment, asserted POSITIVELY: the composer owns no approval path, so the
  // page issued ZERO approval/execution requests. Whether chat is live or killed,
  // free text never approves and never executes.
  expect(
    approvalCalls,
    `free text issued approval/execution calls: ${approvalCalls.join(", ")}`,
  ).toEqual([]);

  // And no confirm control was ever minted from the typed text: a confirm exists
  // only inside a structured ApprovalCard part.
  const composerConfirm = page
    .getByTestId("chat-input")
    .locator("xpath=following::*[@data-testid='confirm-approval']");
  await expect(composerConfirm).toHaveCount(0);

  expectNoRequiredOpFailures(failures);
});

test("journey 4: the blocker workflow is fully reachable on screens with the chat plane down", async ({
  page,
}) => {
  const failures = guardRequiredOps(page, REQUIRED_OP);

  await page.goto("/today");
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("today-queue")).toBeVisible();

  // The seeded UNVERIFIED event is non-actionable, so Today renders its blocked
  // panel instead of a review CTA — the evidence-quality gate, server-derived.
  const blocked = page.getByTestId("event-blocked");
  await expect(blocked).toBeVisible();

  // Today also raises the account-level data-readiness banner with its
  // deep-linked blocker chips (the IA blocker map).
  await expect(page.locator(".banner--warn .filter-chips a").first()).toBeVisible();

  // Follow the blocked event's OWN blocker CTA into the product it blocks.
  await blocked.locator("a.btn").click();
  await expect(page).toHaveURL(new RegExp(`product\\?.*variantId=${VARIANT_B}`));

  // The product detail resolved a real product (never the "no target" empty
  // state) and renders the SERVER-DERIVED `missing` cost readiness as a risk
  // banner — the actual blocker, with its resolution CTA. This is the workflow
  // the old spec never entered.
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.locator(".screen-empty")).toHaveCount(0);
  await expect(page.locator(".banner--risk")).toBeVisible();
  await expect(page.locator(".banner--risk .banner__actions a")).toBeVisible();

  expectNoRequiredOpFailures(failures);
});
