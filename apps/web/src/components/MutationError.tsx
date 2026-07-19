import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";
import { asGatewayError, classifyStatus, type ErrorClass } from "../data/errors";
import { Banner } from "./Banner";
import { LtrToken } from "./LtrToken";

// Shared, localized mutation-error surface (issue #82). A state-changing request
// that FAILS must be visible before the user retries — never a silent fallback
// (runtime "errors are actionable" contract). The title localizes on the HTTP
// status CLASS (400/401/403/409/5xx), an optional per-screen guidance key gives
// the recovery instruction, and the machine `requestId` (LTR-isolated) is shown
// for correlation. Free-text `message`/`detail` is NEVER rendered as copy.
//
// `onRetry` is OFFERED ONLY where the operation is idempotent or current state
// has been re-fetched — an ambiguous outcome (e.g. a 5xx cost commit that may or
// may not have applied) passes no `onRetry`, so the only path forward is an
// explicit re-fetch. `onDismiss` clears just this section's error (a new
// operation also resets it), leaving other sections untouched.

const TITLE_KEY: Record<ErrorClass, MessageKey> = {
  badRequest: "mutationError.title.badRequest",
  unauthorized: "mutationError.title.unauthorized",
  forbidden: "mutationError.title.forbidden",
  conflict: "mutationError.title.conflict",
  server: "mutationError.title.server",
  generic: "mutationError.title.generic",
};

export function MutationError({
  error,
  onDismiss,
  onRetry,
  retryPending = false,
  guidanceKey,
  testId,
}: {
  error: unknown;
  onDismiss: () => void;
  onRetry?: () => void;
  retryPending?: boolean;
  guidanceKey?: MessageKey;
  testId?: string;
}) {
  const t = useT();
  const gw = asGatewayError(error);
  const cls = classifyStatus(gw?.status);

  return (
    <div data-testid={testId} data-error-class={cls} className="mutation-error">
      <Banner
        tone="risk"
        title={t(TITLE_KEY[cls])}
        body={
          <>
            {guidanceKey ? <span>{t(guidanceKey)}</span> : null}
            {gw?.requestId ? (
              <span className="muted mutation-error__requestId">
                {t("mutationError.requestId")} <LtrToken text={gw.requestId} />
              </span>
            ) : null}
          </>
        }
        actions={
          <>
            {onRetry ? (
              <button
                type="button"
                className="btn btn--secondary btn--sm"
                data-testid={testId ? `${testId}-retry` : undefined}
                disabled={retryPending}
                onClick={onRetry}
              >
                {t("mutationError.retry")}
              </button>
            ) : null}
            <button
              type="button"
              className="btn btn--sm"
              data-testid={testId ? `${testId}-dismiss` : undefined}
              onClick={onDismiss}
            >
              {t("mutationError.dismiss")}
            </button>
          </>
        }
      />
    </div>
  );
}
