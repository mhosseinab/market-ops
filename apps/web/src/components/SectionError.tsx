import type { MessageKey } from "@market-ops/locale";
import { useT } from "../app/i18n";

// A per-section runtime-failure state (STATE_MATRIX "error"): a localized,
// ACTIONABLE message with a retry scoped to the single query that failed. It
// exists so a failed SECONDARY query (readiness, cost profiles, observations)
// renders an error the user can recover from — never a legitimate business
// absence ("Not recorded" / "Not available") that only a SUCCESSFUL empty
// response may show (issue #81). Mirrors the shared `.view-error` treatment.
export function SectionError({
  titleKey,
  bodyKey,
  onRetry,
  testId,
  retryDisabled = false,
}: {
  titleKey: MessageKey;
  bodyKey: MessageKey;
  onRetry: () => void;
  testId: string;
  /**
   * Disables the retry control when there is genuinely nothing to re-send — e.g. a
   * failed approval whose bound selection-set version has since gone stale, where a
   * "retry" would have to mint a NEW binding and would therefore be a new consent
   * rather than a recovery. A retry control must always either RETRY or be visibly
   * unavailable; it may never silently dismiss (CLAUDE.md "errors are actionable").
   */
  retryDisabled?: boolean;
}) {
  const t = useT();
  return (
    <div className="view-error" role="alert" data-testid={testId}>
      <p className="view-error__title">{t(titleKey)}</p>
      <p className="view-error__body">{t(bodyKey)}</p>
      <button
        type="button"
        className="btn btn--secondary"
        disabled={retryDisabled}
        onClick={onRetry}
      >
        {t("action.retry")}
      </button>
    </div>
  );
}
