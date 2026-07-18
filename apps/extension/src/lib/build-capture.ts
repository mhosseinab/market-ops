import { CONNECTOR_VERSION } from "./constants";
import type { OwnedTarget } from "./owned-targets";
import { containsSecretKey } from "./redact";
import type { CaptureUpload, ParsedProduct } from "./types";

// The Route B sub-route (OBS-005 / PRD §7.3): every capture is ATTRIBUTED to how
// it was obtained so the core analytics can meter each sub-route against the
// shared Route B budget. `passive` is explicit browsing (EXT-002), `on_demand` a
// user refresh (EXT-003), `watchlist` a bounded scheduled refresh (EXT-012). The
// extension only reports the sub-route it actually used; it can never self-certify
// a different route (that is a server-side determination).
export type CaptureSubRoute = CaptureUpload["subRoute"];

// buildCapture assembles the ALLOW-LISTED capture upload (contracts:
// CaptureUpload, additionalProperties false) from a parsed product and its
// Confirmed owned target. It is the last gate before the wire: it emits ONLY the
// permitted fields (no reviewer/question identity, no session data), stamps the
// parser + connector versions, and attributes the Route B sub-route (OBS-005).
//
// Returns null when the product has no offer (unavailable): a capture is never
// fabricated with an invented price (docs/10 step 3).
export function buildCapture(
  product: ParsedProduct,
  target: OwnedTarget,
  capturedAt: string,
  subRoute: CaptureSubRoute = "passive",
): CaptureUpload | null {
  const offer = product.offer;
  if (offer === null) return null;

  const capture: CaptureUpload = {
    marketplaceAccountId: target.marketplaceAccountId,
    targetId: target.targetId,
    nativeVariantId: offer.nativeVariantId,
    subRoute,
    sourceType: "public-web-endpoint",
    parserVersion: product.parserVersion,
    connectorVersion: CONNECTOR_VERSION,
    evidenceRef: product.canonicalUrl,
    sourceUrl: product.canonicalUrl,
    availabilityStatus: product.availability,
    capturedAt,
    // The extension caps confidence; the server can only degrade it further,
    // never promote it. A full price + in-stock offer is treated as verified;
    // anything less is partially verified.
    confidence:
      offer.price && product.availability === "in_stock" ? "verified" : "partially_verified",
  };
  if (offer.nativeSellerId !== undefined) capture.nativeSellerId = offer.nativeSellerId;
  if (offer.price !== undefined) capture.price = offer.price;
  if (offer.listPrice !== undefined) capture.listPrice = offer.listPrice;
  if (offer.stockSignal !== undefined) capture.stockSignal = offer.stockSignal;

  // Defense in depth: an allow-listed payload must never carry a secret/name-like
  // key. If it somehow does, fail closed (drop the capture) rather than leak.
  if (containsSecretKey(capture)) return null;
  return capture;
}
