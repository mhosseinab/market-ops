import { expect, type Page, test } from "@playwright/test";

// Journey 1 — connect to first value — happy-path smoke against the REAL core
// (compose.test.yml: core + mockdk + postgres, seeded via `task db:reset` +
// `cmd/seede2e`). It is a NON-VACUOUS gate: every assertion is unconditional and
// bound to a SERVER-BACKED behavior, so the gate FAILS whenever the behavior it
// claims to verify is absent (issue #84). It exercises the two never-cut UI
// safety behaviors AND the real onboarding→first-value data seam:
//   • ACC-001 — an Unknown capability NEVER enables the dependent control, and
//     never issues its dependent request. Asserted on a DETERMINISTIC Unknown
//     state (a freshly seeded, never-probed account), unconditionally.
//   • The seeded journey-1 sequence — authenticate → connect → probe transition
//     (catalog_read Unknown → Supported) → catalog sync (the Sync button is
//     actually CLICKED) → a canonical Products row with a server-derived margin
//     readiness — runs end to end against the live gateway (CHAT-009).
//
// The gateway authenticates via an httpOnly session cookie opened in beforeEach
// from the seeded owner (E2E_EMAIL / E2E_PASSWORD, provided by the S32
// kill-switch runner). The mockdk stack serves a deterministic seller-variants
// fixture (deploy/compose.test.yml MOCKDK_CATALOG=1), so the sync imports a
// stable, real set of products the Products screen reads.
//
// WHY EACH DOCUMENTED REPRODUCTION NOW FAILS:
//   • "Unknown that issues a sync": test 1 asserts the sync route received ZERO
//     requests AND the control is disabled — unconditionally (no `if enabled`).
//   • "Inert Sync never clicked": test 2 asserts the durable sync-state reaches
//     `completed`; without the click it stays `none`, so the poll times out.
//   • "500 on a required op" (connect / status / sync / products / readiness):
//     a 4xx/5xx on any required op both (a) trips the response guard and (b)
//     prevents the server-backed assertions (capability → Supported, sync-state
//     → completed, a product ROW, a readiness value) from ever becoming true.
//   • "Products passes on chrome alone": there is no `.toolbar__search` assertion
//     — the pass condition is a real `.data-table__row` plus a server-derived
//     `product-row-readiness` value, which exist ONLY when the data seam loads.

const GATEWAY = process.env.VITE_GATEWAY_BASE_URL ?? "http://localhost:8080";

// Required operations: a 401/403/500 on any of these is a real failure of the
// journey-1 data seam, never an "expected" degraded pass. The guard below fails
// the test if the gateway returns one of those on a matching path.
const REQUIRED_OP = [
  "/auth/login",
  "/auth/me",
  "/connector/status",
  "/connector/connect",
  "/connector/catalog/sync",
  "/catalog/products",
  "/cost/readiness",
];
const HARD_FAIL_STATUS = new Set([401, 403, 500]);

function guardRequiredOps(page: Page): string[] {
  const failures: string[] = [];
  page.on("response", (res) => {
    const url = res.url();
    if (!REQUIRED_OP.some((p) => url.includes(p))) return;
    if (HARD_FAIL_STATUS.has(res.status())) {
      failures.push(`${res.status()} ${url}`);
    }
  });
  return failures;
}

// The capability-gate wrapper around the Sync control exposes the ACC-001 verdict
// as a stable, locale-independent attribute (`data-capability-enabled`).
function syncGate(page: Page) {
  return page.locator(".capability-gate", { has: page.getByTestId("sync-catalog") });
}

test.beforeEach(async ({ context }) => {
  const email = process.env.E2E_EMAIL;
  const password = process.env.E2E_PASSWORD;
  expect(
    email && password,
    "journey-1 is a real-core gate: E2E_EMAIL / E2E_PASSWORD (the seeded owner) are required",
  ).toBeTruthy();
  const res = await context.request.post(`${GATEWAY}/auth/login`, {
    data: { email, password },
  });
  expect(res.ok(), "seeded login should succeed").toBeTruthy();
});

test("ACC-001: an Unknown capability never enables the Sync control and issues ZERO sync calls", async ({
  page,
}) => {
  // Deterministic Unknown state: a freshly seeded account has been authenticated
  // but NEVER connected/probed, so catalog_read has no probe row and resolves to
  // `unknown`. This test runs FIRST (before the connect+sync journey below
  // mutates the account), so the Unknown state is seeded, not incidental.

  // Count every sync request. Aborting keeps the invariant provable even if a UI
  // regression tried to fire one from an Unknown state.
  let syncCalls = 0;
  await page.route("**/connector/catalog/sync", async (route) => {
    syncCalls += 1;
    await route.abort();
  });

  await page.goto("/onboarding");

  // The access-scopes (capability) list renders for the account.
  await expect(page.locator(".capability-list")).toBeVisible();

  // ACC-001, UNCONDITIONAL: catalog_read is not Supported, so its gate reports
  // disabled and the Sync control is disabled — asserted directly, never behind
  // an `if (enabled === "false")` that a Supported state could skip.
  await expect(syncGate(page)).toHaveAttribute("data-capability-enabled", "false");
  const sync = page.getByTestId("sync-catalog");
  await expect(sync).toBeVisible();
  await expect(sync).toBeDisabled();

  // "Unknown never enables dependent logic": no sync request was issued.
  expect(syncCalls, "an Unknown capability must issue no catalog-sync request").toBe(0);
});

test("journey 1: authenticate → connect → probe → sync → a canonical Products row with server-derived readiness", async ({
  page,
}) => {
  test.setTimeout(60_000); // connect + async sync worker + reload polling.
  const failures = guardRequiredOps(page);

  await page.goto("/onboarding");

  // ── Connect: exchange an authorization code for tokens, seeding + probing the
  // capability registry (the mock DK exchange/probe is all-happy). ────────────
  await page.getByTestId("auth-code-input").fill("mock-auth-code");
  await page.getByTestId("connect-submit").click();

  // Probe transition (server-backed): catalog_read goes Unknown → Supported, so
  // the gate flips to enabled and the Sync control becomes clickable. A 401/403
  // on connect/probe would leave this `false`, failing here.
  await expect(syncGate(page)).toHaveAttribute("data-capability-enabled", "true", {
    timeout: 20_000,
  });
  const sync = page.getByTestId("sync-catalog");
  await expect(sync).toBeEnabled();

  // ── Catalog sync: actually CLICK the control (not left inert). ──────────────
  await sync.click();

  // Durable sync evidence (ACC-004/ACC-005): the run advances to `completed`.
  // The status query does not poll, so reload until the durable state settles.
  // Without the click above this stays `none`; a 500 on the sync path never
  // reaches `completed` — either way this poll fails.
  await expect
    .poll(
      async () => {
        await page.reload();
        return page
          .getByTestId("sync-state")
          .getAttribute("data-state")
          .catch(() => null);
      },
      { timeout: 40_000, intervals: [500, 1000, 2000] },
    )
    .toBe("completed");

  // ── Products: the canonical read model now returns the synced variants. ─────
  await page.goto("/products");

  // A genuine loaded state — never the error wrapper or the per-row readiness
  // error (a failed products/readiness load must FAIL here, not degrade to a
  // "passing" chrome-only render).
  await expect(page.locator(".view-error")).toHaveCount(0);
  await expect(page.getByTestId("products-readiness-error")).toHaveCount(0);

  // A CONTRACT-BACKED product ROW exists (Product/Variant/Owned Offer) — this is
  // the real data seam, present only because connect→sync imported the catalog.
  await expect(page.locator(".data-table__row").first()).toBeVisible();
  expect(await page.locator(".data-table__row").count()).toBeGreaterThan(0);

  // A SERVER-DERIVED margin readiness (CST-003) is rendered on the row — asserted
  // via the locale-independent `data-state`, not localized copy. Its presence
  // proves `/cost/readiness` resolved a real verdict for a real variant.
  const readiness = page.getByTestId("product-row-readiness").first();
  await expect(readiness).toBeVisible();
  await expect(readiness).toHaveAttribute("data-state", /^(complete|partial|stale|missing)$/);

  // No required operation returned 401/403/500 anywhere in the journey.
  expect(failures, `required-op failures: ${failures.join(", ")}`).toEqual([]);
});
