import { en, faIR, PSEUDO_CLOSE, PSEUDO_OPEN } from "@market-ops/locale";
import { render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { I18nProvider } from "../../app/i18n";
import {
  resetUnsupportedValueSink,
  setUnsupportedValueSink,
  type UnsupportedValueReport,
} from "../../app/unsupportedTelemetry";
import type { ChatFailure } from "../types";
import { FailureView } from "./DockMessageView";

// LOC-002 (#121): a §12.4 structured failure renders its localized summary from
// the stable `failure.code` mapped to a CLOSED catalog key. The server
// `failure.message` (a machine diagnostic that may carry English/internal text)
// is NEVER rendered as authoritative localized copy. An unknown code renders the
// catalog-backed unavailable label + PII-free drift telemetry.

// A distinctive server diagnostic we assert is NEVER shown to the user.
const RAW_MESSAGE = "raw-server-diagnostic-DO-NOT-RENDER";

function renderFailure(failure: ChatFailure, pseudo = false, locale: "en" | "fa-IR" = "en") {
  const wrapper = ({ children }: { children: ReactNode }) => (
    <I18nProvider initialLocale={locale} pseudo={pseudo}>
      {children}
    </I18nProvider>
  );
  return render(<FailureView failure={failure} />, { wrapper });
}

afterEach(() => {
  resetUnsupportedValueSink();
  document.documentElement.removeAttribute("dir");
  document.documentElement.removeAttribute("lang");
});

describe("FailureView — LOC-002 closed failure-code mapping", () => {
  it("renders the localized code label, NEVER the server message, for a supported code", () => {
    renderFailure({ code: "TOOL_TIMEOUT", message: RAW_MESSAGE });

    const body = screen.getByText(en["chat.failure.toolTimeout"]);
    expect(body).toBeInTheDocument();
    // The raw server diagnostic is never authoritative copy.
    expect(screen.queryByText(RAW_MESSAGE)).toBeNull();
    expect(document.body.textContent ?? "").not.toContain(RAW_MESSAGE);
  });

  it("resolves the supported-code label under fa-IR", () => {
    renderFailure({ code: "MODEL_PROVIDER_ERROR", message: RAW_MESSAGE }, false, "fa-IR");
    expect(screen.getByText(faIR["chat.failure.providerError"])).toBeInTheDocument();
    expect(document.body.textContent ?? "").not.toContain(RAW_MESSAGE);
  });

  it("an unknown code renders the localized unavailable label + telemetry, never the raw value", () => {
    const reports: UnsupportedValueReport[] = [];
    setUnsupportedValueSink((r) => reports.push(r));

    renderFailure({ code: "TOTALLY_NEW_CODE", message: RAW_MESSAGE });

    expect(screen.getByText(en["chat.failure.unsupported"])).toBeInTheDocument();
    // Neither the raw code nor the server message appears as copy.
    expect(document.body.textContent ?? "").not.toContain("TOTALLY_NEW_CODE");
    expect(document.body.textContent ?? "").not.toContain(RAW_MESSAGE);
    expect(reports).toHaveLength(1);
    expect(reports[0]).toMatchObject({ kind: "chat_failure_code", value: "TOTALLY_NEW_CODE" });
  });

  it("resolves copy through the catalog under the pseudo pack (LOC-011)", () => {
    renderFailure({ code: "TOKEN_CEILING", message: RAW_MESSAGE }, true);
    const body = screen.getByTestId("chat-failure").querySelector(".chat-failure__body");
    expect(body?.textContent ?? "").toContain(PSEUDO_OPEN);
    expect(body?.textContent ?? "").toContain(PSEUDO_CLOSE);
    expect(document.body.textContent ?? "").not.toContain(RAW_MESSAGE);
  });
});
