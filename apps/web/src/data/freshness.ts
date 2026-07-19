// Thin re-export of the shared freshness derivation (packages/locale). Web
// modules import freshness from HERE so the SPA and the extension overlay
// derive from the ONE source of truth (OBS-004 / EXT-005 parity) — never a
// locally duplicated threshold. Behaviour is unchanged except the OBS-004
// correctness fix (deadline-driven, not a fixed 60m/6h age threshold).
export {
  FRESHNESS_AGING_MAX_MINUTES,
  FRESHNESS_FRESH_MAX_MINUTES,
  type FreshnessInput,
  type FreshnessState,
  freshnessState,
  freshnessStateFromAge,
  freshnessTransitions,
} from "@market-ops/locale";
