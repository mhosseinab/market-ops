import { expect, type Page } from "@playwright/test";

// Shared real-core journey-gate helpers (issue #84 / the S32 duplicate-root
// expansion).
//
// These journeys are NON-VACUOUS gates: they run against the REAL core
// (tools/integration/run_killswitch_journey.sh → deploy/compose.test.yml) over a
// database seeded by `task db:reset`, and every claimed assertion is
// UNCONDITIONAL. A gate that cannot go red is the defect being fixed here, so
// nothing below may be written as "assert X, or accept that X is absent".
//
// The ids are the deterministic rows in services/core/fixtures/dev_seed.sql.
// They are the contract between the seed and these specs: if a fixture row is
// removed, the assertions bound to it FAIL (they do not skip), which is exactly
// the regression behavior the gate exists to provide.

export const GATEWAY = process.env.VITE_GATEWAY_BASE_URL ?? "http://localhost:8080";

/** The seeded marketplace account (dev_seed.sql; also the web app's default). */
export const ACCOUNT_ID = "00000000-0000-0000-0000-000000000003";

/** Variant A — verified evidence, complete readiness, live approval control. */
export const VARIANT_A = "00000000-0000-0000-0000-0000000000b1";
/** Variant B — unverified evidence, missing cost ⇒ the blocker lane. */
export const VARIANT_B = "00000000-0000-0000-0000-0000000000b2";
/** Variant C — the executable bulk candidate (its own live card). */
export const VARIANT_C = "00000000-0000-0000-0000-0000000000b3";

/** Event A — verified ⇒ ACTIONABLE ⇒ Today renders its "review" CTA. */
export const EVENT_A = "00000000-0000-0000-0000-000000000201";
/** Event B — unverified ⇒ NON-actionable ⇒ Today renders its blocked panel. */
export const EVENT_B = "00000000-0000-0000-0000-000000000202";

/**
 * Variant C's seeded observed-offer identity (native variant id + seller). The
 * bulk screen keys its per-candidate include control on the OFFER identity
 * (OBS-004), so this is what addresses that row's control.
 */
export const OFFER_C_IDENTITY = "9000013:seller-journey-c";

/** Variant A's approval card — journey 2 confirms THIS one. */
export const CARD_A = "00000000-0000-0000-0000-000000000321";
/** Variant C's approval card — journey 3's bulk selection member. */
export const CARD_C = "00000000-0000-0000-0000-000000000322";

/** Variant A's recommendation (the card's PRC-001 record). */
export const RECOMMENDATION_A = "00000000-0000-0000-0000-000000000301";

// A 401/403/500 on a REQUIRED journey operation is a real failure of the data
// seam, never an "expected" degraded pass. Callers list the ops their journey
// genuinely depends on and assert the collected failures are empty at the end.
const HARD_FAIL_STATUS = new Set([401, 403, 500]);

/**
 * Collect 401/403/500 responses on any of `requiredOps` for the life of `page`.
 * The returned array is filled as the journey runs; assert it is empty LAST, so
 * a failure anywhere in the journey fails the test.
 */
export function guardRequiredOps(page: Page, requiredOps: readonly string[]): string[] {
  const failures: string[] = [];
  page.on("response", (res) => {
    const url = res.url();
    if (!requiredOps.some((p) => url.includes(p))) return;
    if (HARD_FAIL_STATUS.has(res.status())) {
      failures.push(`${res.status()} ${url}`);
    }
  });
  return failures;
}

/** Assert no required operation failed. Call at the END of a journey. */
export function expectNoRequiredOpFailures(failures: readonly string[]): void {
  expect(failures, `required-op failures: ${failures.join(", ")}`).toEqual([]);
}

/**
 * Open an authenticated session as the seeded owner. UNCONDITIONAL: these are
 * real-core gates, so absent credentials FAIL rather than silently skipping the
 * authentication (and with it every authorized read the journey depends on).
 */
export async function loginAsSeededOwner(context: {
  request: { post: (url: string, opts: { data: unknown }) => Promise<{ ok: () => boolean }> };
}): Promise<void> {
  const email = process.env.E2E_EMAIL;
  const password = process.env.E2E_PASSWORD;
  expect(
    email && password,
    "real-core journey gate: E2E_EMAIL / E2E_PASSWORD (the seeded owner) are required",
  ).toBeTruthy();
  const res = await context.request.post(`${GATEWAY}/auth/login`, {
    data: { email, password },
  });
  expect(res.ok(), "seeded login should succeed").toBeTruthy();
}
