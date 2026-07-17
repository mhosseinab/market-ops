import type { ReactNode } from "react";
import { useT } from "../app/i18n";
import { Skeleton } from "./Skeleton";

// Shared loading / empty / error wrapper keyed off a per-view fetch status
// (STATE_MATRIX: "implement loading/empty as shared wrapper components keyed off
// a per-view fetch status"). All copy resolves through the catalog; the error
// state offers a retry, the empty state is the reassuring "nothing to show".
export function ViewState({
  pending,
  error,
  isEmpty = false,
  onRetry,
  skeletonRows,
  children,
}: {
  pending: boolean;
  error: boolean;
  isEmpty?: boolean;
  onRetry?: () => void;
  skeletonRows?: number;
  children: ReactNode;
}) {
  const t = useT();

  if (pending) {
    // The output element carries an implicit ARIA status live region for the fetch.
    return (
      <output aria-label={t("state.loading")} className="view-loading">
        <Skeleton rows={skeletonRows} />
      </output>
    );
  }

  if (error) {
    return (
      <div className="view-error" role="alert">
        <p className="view-error__title">{t("state.error.title")}</p>
        <p className="view-error__body">{t("state.error.body")}</p>
        {onRetry ? (
          <button type="button" className="btn btn--secondary" onClick={onRetry}>
            {t("action.retry")}
          </button>
        ) : null}
      </div>
    );
  }

  if (isEmpty) {
    return (
      <div className="screen-empty">
        <p>{t("state.empty.title")}</p>
        <p>{t("state.empty.body")}</p>
      </div>
    );
  }

  return <>{children}</>;
}
