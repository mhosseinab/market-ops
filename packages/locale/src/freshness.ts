// Freshness banding thresholds (minutes since capture). This is the SINGLE
// source of truth both the SPA (apps/web/src/components/badges.tsx
// FreshnessPill) and the extension (apps/extension/src/lib/overlay-data.ts
// freshnessBucketOf) import — the extension's overlay must render values
// EQUAL to the Market screen (EXT-005), so the banding logic lives in the ONE
// shared package both already depend on rather than being duplicated and
// risking silent drift.
export const FRESHNESS_FRESH_MAX_MINUTES = 60;
export const FRESHNESS_AGING_MAX_MINUTES = 360;
