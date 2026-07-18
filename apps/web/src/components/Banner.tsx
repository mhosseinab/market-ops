import type { ReactNode } from "react";

// Banner (component inventory): blocked / disconnected / conflicted / invalidated
// surfaces. Tone carries meaning but never stands alone — the title/body text
// always accompanies it. Copy is passed in as already-translated nodes so the
// banner stays a pure layout primitive.
export type BannerTone = "risk" | "warn" | "conflict" | "info";

const TONE_CLASS: Record<BannerTone, string> = {
  risk: "banner--risk",
  warn: "banner--warn",
  conflict: "banner--conflict",
  info: "banner--info",
};

export function Banner({
  tone,
  title,
  body,
  actions,
}: {
  tone: BannerTone;
  title: ReactNode;
  body?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <div className={`banner ${TONE_CLASS[tone]}`} role="alert">
      <div className="banner__body">
        <p className="banner__title">{title}</p>
        {body ? <p className="banner__text">{body}</p> : null}
      </div>
      {actions ? <div className="banner__actions">{actions}</div> : null}
    </div>
  );
}
