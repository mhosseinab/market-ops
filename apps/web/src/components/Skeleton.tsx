// Loading skeleton (STATE_MATRIX shared loading pattern). Pure presentation —
// pulsing panel blocks; no copy, so nothing to localize.
export function Skeleton({ rows = 4 }: { rows?: number }) {
  return (
    <div className="skeleton" aria-hidden>
      {Array.from({ length: rows }, (_, i) => (
        // biome-ignore lint/suspicious/noArrayIndexKey: fixed-count static placeholders, never reordered
        <div key={i} className="skeleton__block" />
      ))}
    </div>
  );
}
