import type { MessageKey } from "@market-ops/locale";
import { useLocale, useT } from "../app/i18n";
import { formatCount } from "../data/format";

// CoverageBars (component inventory): freshness-coverage segments (Market screen).
// Each segment pairs a semantic tone with a text label and a count — color never
// stands alone. Widths are a share of the total; when the total is zero the bars
// render empty rather than a fabricated share (PRC-001). Percentages are computed
// from surfaced counts only, never from money or inferred values.

export interface CoverageSegment {
  readonly id: string;
  readonly labelKey: MessageKey;
  readonly tone: "pos" | "warn" | "risk" | "info" | "ink2";
  readonly count: number;
}

export function CoverageBars({ segments }: { segments: readonly CoverageSegment[] }) {
  const t = useT();
  const { locale } = useLocale();
  const total = segments.reduce((sum, s) => sum + s.count, 0);
  return (
    <div className="coverage" data-testid="coverage-bars">
      {segments.map((seg) => {
        const share = total > 0 ? Math.round((seg.count / total) * 100) : 0;
        return (
          <div className="coverage__row" key={seg.id} data-segment={seg.id}>
            <div className="coverage__head">
              <span className="coverage__label">
                <span className="badge__dot" data-tone={seg.tone} aria-hidden />
                {t(seg.labelKey)}
              </span>
              <span className="coverage__count">{formatCount(seg.count, locale)}</span>
            </div>
            <div className="coverage__track" aria-hidden>
              <div
                className="coverage__fill"
                data-tone={seg.tone}
                style={{ inlineSize: `${share}%` }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}
