import { useEffect, useState } from "react";

// useNow — a clock that advances to the SAME instant every freshness-derived
// surface reads (OBS-004). A page left open must transition to Stale exactly
// at an offer's authoritative deadline without navigation, and a suspended
// tab must reconcile the moment it resumes.
//
// It samples `Date.now()` and re-samples via a SINGLE self-rescheduling timer
// aimed at the NEAREST FUTURE transition (not a coarse interval), plus `focus`
// and `visibilitychange` listeners that reconcile immediately after a tab was
// backgrounded (timers throttle/pause while hidden). Callers MUST memoize the
// `transitions` array so the effect only re-runs when the schedule changes.
export function useNow(transitions: readonly number[]): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    let timer: ReturnType<typeof setTimeout> | undefined;

    const reconcile = () => setNow(Date.now());

    const schedule = () => {
      if (timer !== undefined) clearTimeout(timer);
      const current = Date.now();
      let next = Number.POSITIVE_INFINITY;
      for (const t of transitions) {
        if (t > current && t < next) next = t;
      }
      if (next === Number.POSITIVE_INFINITY) return;
      timer = setTimeout(() => {
        setNow(Date.now());
        schedule();
      }, next - current);
    };

    const onResume = () => {
      reconcile();
      schedule();
    };

    schedule();
    window.addEventListener("focus", onResume);
    document.addEventListener("visibilitychange", onResume);
    return () => {
      if (timer !== undefined) clearTimeout(timer);
      window.removeEventListener("focus", onResume);
      document.removeEventListener("visibilitychange", onResume);
    };
  }, [transitions]);

  return now;
}
