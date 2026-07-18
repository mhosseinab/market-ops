// Connector + parser identity (docs/14 observability: semver connector versions;
// parser version STAMPED on every capture so a parser-drift event (§10.4) is
// attributable, never silent). Bump the parser version on ANY change to the
// product parser's field selection or normalization.
export const PARSER_VERSION = "dk-product@1.0.0";
export const CONNECTOR_VERSION = "market-ops-ext@0.1.0";

// Canonical schema version the extension emits (additive within a major; a
// breaking change requires a major bump + backend migration — docs/14).
export const SCHEMA_VERSION = 1;

// Only Digikala product pages are captured. Passive capture happens ONLY during
// explicit product browsing (EXT-002, §12): a page that does not match this is
// never fetched or parsed.
export const PRODUCT_PATH_RE = /^\/product\/dkp-(\d+)\//;

// The verified public product-detail endpoint (docs/04). The content script
// fetches ONLY this endpoint for the product the user is actively viewing.
export function productApiUrl(productId: number): string {
  return `https://api.digikala.com/v2/product/${productId}/`;
}

// Raw money is stored as Rial with this unit token; Toman is derived server-side
// (Rial ÷ 10). The extension never guesses a unit (docs/11).
export const RIAL_UNIT = "IRR-rial";

// Bounded offline queue (docs/09: enforce a queue cap with a metric). Beyond the
// cap the oldest pending item is shed and a backpressure metric is emitted —
// queues never grow unbounded.
export const QUEUE_CAP = 200;

// Bounded retry backoff (docs/10 failure recovery: conservative, not observed DK
// behaviour). Attempts beyond MAX_ATTEMPTS park the item as failed (visible in
// the popup), never an infinite retry loop.
export const MAX_ATTEMPTS = 5;
export const BASE_BACKOFF_MS = 2000;
