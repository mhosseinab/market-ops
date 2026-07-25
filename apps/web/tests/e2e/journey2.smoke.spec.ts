import { expect, test } from "@playwright/test";
import {
  CARD_A,
  EVENT_A,
  expectNoRequiredOpFailures,
  guardRequiredOps,
  loginAsSeededOwner,
  VARIANT_A,
} from "./fixtures";

// Journey 2 — daily decision on screens — a NON-VACUOUS smoke against the REAL
// core (seeded via `task db:reset`, services/core/fixtures/dev_seed.sql). It
// drives the ranked Today → event detail → recommendation → structured approval
// chain, and every assertion below is UNCONDITIONAL and bound to SERVER-BACKED
// data (issue #84 / the S32 duplicate-root expansion).
//
// WHAT THIS GATE USED TO DO, AND WHY IT WAS VACUOUS: it accepted
// `today-no-action` OR `today-queue`, entered the event chain only
// `if (await review.count())`, and accepted the recommendation surface's
// `.screen-empty` no-card fallback. With a seed that carried only org/user/
// account rows, EVERY one of those escape hatches was taken — the suite passed
// green while never once entering the daily-decision path it claims to verify.
//
// WHY IT NOW FAILS WHEN THE BEHAVIOR IS ABSENT:
//   • Remove the seeded EVENT ⇒ `today-queue` never renders (Today falls to
//     `today-no-action`), and `event-review` never appears ⇒ FAIL, not skip.
//   • Remove the seeded CARD ⇒ `/recommendation?cardId=…` renders the no-card
//     `.screen-empty`, so `approval-card` never becomes visible ⇒ FAIL.
//   • A 401/403/500 on any required op both trips the response guard AND
//     prevents the server-backed assertions from ever becoming true.
//   • There is no assertion that a bare shell or nav chrome can satisfy: the
//     pass condition is a real event row, a real approval card, and the real
//     recommend-only terminal reached by CLICKING the structured control.
//
// KNOWN P0 GAP (carry-forward, not weakened here): the event detail's
// "see recommendation" CTA deep-links with `variantId`, but the Recommendation
// screen resolves a card by `cardId`/`recommendationId` only — the P0 gateway
// exposes no event→card linkage. This spec therefore asserts the CTA and its
// real deep-link target UNCONDITIONALLY, then reaches the card by its
// deterministic seeded id. Closing that linkage is an api_data_contracts /
// go_domain_executor concern (a `/recommendations?variantId=` or an event→card
// reference); until it lands, THIS is the honest reachable chain.

const REQUIRED_OP = [
  "/auth/login",
  "/auth/me",
  "/today",
  "/event",
  "/approvals/card",
  "/recommendations/detail",
  "/approvals/confirm",
];

test.beforeEach(async ({ context }) => {
  await loginAsSeededOwner(context);
});

test("journey 2: ranked Today → event detail → the seeded recommendation's structured approval surface", async ({
  page,
}) => {
  const failures = guardRequiredOps(page, REQUIRED_OP);

  await page.goto("/today");

  // A GENUINE loaded state — never the generic error wrapper.
  await expect(page.locator(".view-error")).toHaveCount(0);

  // The ranked queue itself, NOT `today-no-action`: the seeded events must be
  // there. This is the assertion that dies when the fixture is removed.
  await expect(page.getByTestId("today-queue")).toBeVisible();

  // Both seeded events are ranked and rendered (actionable A + blocked B).
  await expect(page.getByTestId("event-row")).toHaveCount(2);

  // Event A is ACTIONABLE (verified evidence), so it carries the review CTA.
  const review = page.getByTestId("event-review").first();
  await expect(review).toBeVisible();
  await review.click();

  // The event detail is a real server read bound to the seeded event id.
  await expect(page).toHaveURL(new RegExp(`eventId=${EVENT_A}`));
  await expect(page.locator(".view-error")).toHaveCount(0);

  // The CTA into the recommendation exists and deep-links the event's variant.
  const toRec = page.getByTestId("event-to-recommendation");
  await expect(toRec).toBeVisible();
  await toRec.click();
  await expect(page).toHaveURL(new RegExp(`recommendation\\?.*variantId=${VARIANT_A}`));

  // Reach the live control by its deterministic seeded card id (see the P0 gap
  // note above). The card MUST render — no `.screen-empty` fallback accepted.
  await page.goto(`/recommendation?cardId=${CARD_A}`);
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("approval-card")).toBeVisible();

  // Server-backed PRC-001 fields render from the authoritative detail read —
  // proof the recommendation seam resolved, not just that a card shell drew.
  await expect(page.getByTestId("prc-fields")).toBeVisible();
  await expect(page.getByTestId("prc-currentPrice")).toBeVisible();
  await expect(page.getByTestId("prc-readiness")).toBeVisible();

  // The APR-001 bound versions come from the card itself.
  await expect(page.getByTestId("prc-inputs")).toBeVisible();

  expectNoRequiredOpFailures(failures);
});

test("journey 2: only the structured control approves, and confirming lands recommend-only (EXE-005)", async ({
  page,
}) => {
  const failures = guardRequiredOps(page, REQUIRED_OP);

  await page.goto(`/recommendation?cardId=${CARD_A}`);
  await expect(page.locator(".view-error")).toHaveCount(0);

  // The card and its containment footnote are present — unconditionally.
  await expect(page.getByTestId("approval-card")).toBeVisible();
  await expect(page.getByTestId("approval-footnote")).toBeVisible();

  // The approval control is a STRUCTURED BUTTON (§8 free-text containment) and
  // is LIVE, because the seeded card is `awaiting_confirmation` with a control.
  const confirm = page.getByTestId("confirm-approval");
  await expect(confirm).toHaveJSProperty("tagName", "BUTTON");
  await expect(confirm).toBeEnabled();

  // Confirming through that control reaches the recommend-only terminal:
  // Approved / awaiting external execution (EXE-005). Unconditional — this is
  // the branch the old `if (await confirm.isEnabled())` could silently skip.
  await confirm.click();
  await expect(page.getByTestId("recommend-only")).toBeVisible();

  expectNoRequiredOpFailures(failures);
});
