import { DEFAULT_LOCALE, faIR } from "@market-ops/locale";
import { fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Providers } from "../app/Providers";
import { GatewayError } from "../data/errors";
import { ACCOUNT_ID } from "../test/msw/fixtures";
import { MutationError } from "./MutationError";

afterEach(() => {
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

function renderError(node: ReactNode) {
  return render(
    <Providers initialLocale={DEFAULT_LOCALE} marketplaceAccountId={ACCOUNT_ID}>
      {node}
    </Providers>,
  );
}

const STATUS_TITLES: Array<[number, string]> = [
  [400, faIR["mutationError.title.badRequest"]],
  [401, faIR["mutationError.title.unauthorized"]],
  [403, faIR["mutationError.title.forbidden"]],
  [409, faIR["mutationError.title.conflict"]],
  [500, faIR["mutationError.title.server"]],
];

describe("MutationError (issue #82 — shared failure surface)", () => {
  it.each(STATUS_TITLES)("renders the localized title for HTTP %s", (status, title) => {
    renderError(
      <MutationError
        testId="e"
        error={new GatewayError({ code: "X" }, status)}
        onDismiss={() => {}}
      />,
    );
    expect(screen.getByText(title)).toBeInTheDocument();
  });

  it("shows the correlation requestId (LTR-isolated) when present, never the free-text message", () => {
    renderError(
      <MutationError
        testId="e"
        error={
          new GatewayError({ code: "X", message: "raw server detail", requestId: "req-42" }, 500)
        }
        onDismiss={() => {}}
      />,
    );
    expect(screen.getByText("req-42")).toBeInTheDocument();
    // Free-text message/detail is diagnostic only — never rendered as copy.
    expect(screen.queryByText("raw server detail")).toBeNull();
  });

  it("offers Retry only when onRetry is supplied; dismiss always clears", () => {
    const onRetry = vi.fn();
    const onDismiss = vi.fn();
    const { rerender } = renderError(
      <MutationError
        testId="e"
        error={new GatewayError({}, 500)}
        onDismiss={onDismiss}
        onRetry={onRetry}
      />,
    );
    fireEvent.click(screen.getByTestId("e-retry"));
    expect(onRetry).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("e-dismiss"));
    expect(onDismiss).toHaveBeenCalledTimes(1);

    // An ambiguous outcome passes no onRetry: the Retry control is ABSENT.
    rerender(
      <Providers initialLocale={DEFAULT_LOCALE} marketplaceAccountId={ACCOUNT_ID}>
        <MutationError testId="e" error={new GatewayError({}, 500)} onDismiss={onDismiss} />
      </Providers>,
    );
    expect(screen.queryByTestId("e-retry")).toBeNull();
    expect(screen.getByTestId("e-dismiss")).toBeInTheDocument();
  });

  it("classifies an unknown (network) error as the generic failure", () => {
    renderError(<MutationError testId="e" error={new Error("network")} onDismiss={() => {}} />);
    expect(screen.getByText(faIR["mutationError.title.generic"])).toBeInTheDocument();
  });
});
