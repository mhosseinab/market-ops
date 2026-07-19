import { useParams } from "@tanstack/react-router";
import { useT } from "../app/i18n";
import { runbookContent } from "../app/runbookContent";
import { runbookBySlug } from "../app/runbooks";
import { ViewState } from "../components/ViewState";
import { useSession } from "../data/hooks";

// In-SPA runbook viewer (OPS-002 / PRD §20.1). Reached only via the Operations
// runbook deep links, which derive from the canonical registry (app/runbooks.ts).
// It sits behind the SAME Internal-role gate as Operations: a non-internal
// principal never sees a runbook body. The slug is a TECHNICAL identifier
// (LTR-isolated, never localized); the visible chrome comes from catalog keys.
// Content is repo markdown rendered as escaped preformatted TEXT — no HTML
// injection. An unknown slug degrades to a not-found EmptyState (STATE_MATRIX).
export function RunbookViewer() {
  const t = useT();
  const sessionQuery = useSession();
  const { slug } = useParams({ strict: false }) as { slug?: string };

  const role = sessionQuery.data?.role;

  if (sessionQuery.isPending) {
    return (
      <div className="screen">
        <ViewState pending error={false}>
          <span />
        </ViewState>
      </div>
    );
  }

  if (role !== "internal") {
    return (
      <div className="screen">
        <div className="screen-empty" data-testid="operations-internal-only">
          <p>{t("operations.internalOnly.title")}</p>
          <p>{t("operations.internalOnly.body")}</p>
        </div>
      </div>
    );
  }

  const entry = slug ? runbookBySlug(slug) : undefined;
  const content = entry ? runbookContent(entry.file) : undefined;

  if (!entry || content === undefined) {
    return (
      <div className="screen">
        <div className="screen-empty" data-testid="runbook-not-found">
          <p>{t("runbook.viewer.notFound.title")}</p>
          <p>{t("runbook.viewer.notFound.body")}</p>
        </div>
      </div>
    );
  }

  return (
    <div className="screen">
      <div className="runbook-viewer">
        <h1 className="runbook-viewer__heading">{t("runbook.viewer.heading")}</h1>
        <p className="runbook-viewer__meta">
          <span className="muted">{t("runbook.viewer.slugLabel")}</span>{" "}
          {/* Technical identifier: force LTR isolation inside any RTL layout. */}
          <bdi dir="ltr" className="code">
            {entry.slug}
          </bdi>
        </p>
        {/* Repo markdown as escaped text; dir=ltr since it is a technical artifact. */}
        <pre className="runbook-viewer__body" data-testid="runbook-content" dir="ltr">
          {content}
        </pre>
      </div>
    </div>
  );
}
