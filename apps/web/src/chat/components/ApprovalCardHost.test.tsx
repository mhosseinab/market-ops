import { DEFAULT_LOCALE } from "@market-ops/locale";
import { QueryClient } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import { HttpResponse, http } from "msw";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Providers } from "../../app/Providers";
import { queryKeys } from "../../data/hooks";
import type { ApprovalCardView } from "../../data/types";
import { ACCOUNT_ID, approvalCardAwaiting, CARD_ID } from "../../test/msw/fixtures";
import { BASE } from "../../test/msw/handlers";
import { server } from "../../test/msw/server";
import { ApprovalCardHost } from "./ApprovalCardHost";

// The deep-link chip renders a TanStack Router <Link>, which needs a RouterProvider
// this isolated host test does not mount. It is orthogonal to the §8.1 gating under
// test, so stub it out to render the host without a full router.
vi.mock("./DeepLinkButton", () => ({ DeepLinkButton: () => null }));

// §8.1 cached-control non-reuse on the S29 chat dock. TanStack Query serves cached
// card data SYNCHRONOUSLY on a remount/restore while the authoritative refetch runs
// in the background (isPending is already false). The host must WITHHOLD every
// executable control — binding, hasControl, Confirm — until a read has settled for
// THIS mount; cached data may back only a non-actionable loading skeleton. A stale
// executable approval control must never reach the reused S27 ApprovalCard.

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

function renderHost(cardId: string, seed?: ApprovalCardView) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  if (seed) queryClient.setQueryData(queryKeys.approvalCard(cardId), seed);
  const utils = render(
    <Providers
      initialLocale={DEFAULT_LOCALE}
      marketplaceAccountId={ACCOUNT_ID}
      queryClient={queryClient}
    >
      <ApprovalCardHost cardId={cardId} />
    </Providers>,
  );
  return utils;
}

const CARD_B_ID = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee";

describe("ApprovalCardHost — §8.1 withhold cached control until authoritative refetch", () => {
  it("a cached awaiting-confirmation card remounted after server expiry never renders Confirm before the fresh response", () => {
    // The browser cache still holds the live control; the server has since expired it.
    const expired: ApprovalCardView = {
      ...approvalCardAwaiting,
      state: "expired",
      hasControl: false,
    };
    server.use(http.get(`${BASE}/approvals/card`, () => HttpResponse.json(expired)));

    const { container } = renderHost(CARD_ID, approvalCardAwaiting);

    // FIRST render (authoritative read not yet settled): no executable control and
    // no reused approval card may exist — only the non-actionable loading skeleton.
    expect(screen.queryByTestId("confirm-approval")).toBeNull();
    expect(screen.queryByTestId("approval-card")).toBeNull();
    expect(container.querySelector(".view-loading")).not.toBeNull();
  });

  it("after the authoritative expired response arrives, the reused card is present but non-actionable", async () => {
    const expired: ApprovalCardView = {
      ...approvalCardAwaiting,
      state: "expired",
      hasControl: false,
    };
    server.use(http.get(`${BASE}/approvals/card`, () => HttpResponse.json(expired)));

    renderHost(CARD_ID, approvalCardAwaiting);

    const confirm = await screen.findByTestId("confirm-approval");
    expect(confirm).toBeDisabled();
  });

  it("a fresh awaiting-confirmation response enables exactly one control bound to its returned version", async () => {
    server.use(http.get(`${BASE}/approvals/card`, () => HttpResponse.json(approvalCardAwaiting)));

    renderHost(CARD_ID);

    const confirm = await screen.findByTestId("confirm-approval");
    expect(screen.getAllByTestId("confirm-approval")).toHaveLength(1);
    expect(confirm).toBeEnabled();
    const card = screen.getByTestId("approval-card");
    expect(card).toHaveAttribute("data-card-version", String(approvalCardAwaiting.version));
    expect(card).toHaveAttribute("data-baseline-version", String(approvalCardAwaiting.version));
    expect(card).toHaveAttribute("data-stale", "false");
  });

  it("a fetch error leaves the card unavailable and non-actionable", async () => {
    server.use(
      http.get(`${BASE}/approvals/card`, () =>
        HttpResponse.json({ code: "boom", message: "boom" }, { status: 500 }),
      ),
    );

    renderHost(CARD_ID, approvalCardAwaiting);

    await screen.findByRole("alert");
    expect(screen.queryByTestId("confirm-approval")).toBeNull();
    expect(screen.queryByTestId("approval-card")).toBeNull();
  });

  it("switching card IDs on a reused host cannot inherit another card's approval baseline", async () => {
    const cardB: ApprovalCardView = { ...approvalCardAwaiting, id: CARD_B_ID, version: 9 };
    server.use(
      http.get(`${BASE}/approvals/card`, ({ request }) => {
        const id = new URL(request.url).searchParams.get("cardId");
        return HttpResponse.json(id === CARD_B_ID ? cardB : approvalCardAwaiting);
      }),
    );

    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const { rerender } = render(
      <Providers
        initialLocale={DEFAULT_LOCALE}
        marketplaceAccountId={ACCOUNT_ID}
        queryClient={queryClient}
      >
        <ApprovalCardHost cardId={CARD_ID} />
      </Providers>,
    );

    // Card A settles; its baseline anchors to A's version.
    await waitFor(() =>
      expect(screen.getByTestId("approval-card")).toHaveAttribute("data-card-version", "1"),
    );

    // Reuse the same host instance for a DIFFERENT card ID.
    rerender(
      <Providers
        initialLocale={DEFAULT_LOCALE}
        marketplaceAccountId={ACCOUNT_ID}
        queryClient={queryClient}
      >
        <ApprovalCardHost cardId={CARD_B_ID} />
      </Providers>,
    );

    // Card B must anchor to ITS OWN version, never carry A's baseline (which would
    // spuriously flag B as stale).
    await waitFor(() =>
      expect(screen.getByTestId("approval-card")).toHaveAttribute("data-card-version", "9"),
    );
    const card = screen.getByTestId("approval-card");
    expect(card).toHaveAttribute("data-baseline-version", "9");
    expect(card).toHaveAttribute("data-stale", "false");
  });
});
