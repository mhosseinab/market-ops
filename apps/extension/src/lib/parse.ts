import { PARSER_VERSION, RIAL_UNIT } from "./constants";
import { canonicalProductUrl, normalizePersian } from "./normalize";
import type { AvailabilityStatus, ParsedOffer, ParseResult, RawAmount } from "./types";

// parseProductResponse maps the verified public product-detail response
// (GET /v2/product/{id}/, docs/04) onto a normalized, allow-listed ParsedProduct.
// It is the ONLY place DK response keys are read; a missing top-level key is a
// structured parser-drift reason (§10.4), never a thrown error or a silent guess.
//
// Faithful to the selector/field contract (docs/06, docs/07):
//   product.id                          -> native product id
//   product.url.uri                     -> canonical URL (slug-stripped)
//   product.title_fa                    -> title (NFC)
//   product.status ('marketable'|...)   -> availability (never inferred from price)
//   variants[].id / default_variant.id  -> native variant id (offer identity)
//   variants[].seller.id                -> native seller id
//   variants[].price.selling_price      -> RAW Rial price evidence
//   variants[].price.rrp_price          -> RAW Rial list price evidence
//   variants[].price.marketable_stock   -> stock SIGNAL (not exact inventory)
export function parseProductResponse(raw: unknown): ParseResult {
  if (raw === null || typeof raw !== "object") {
    return { ok: false, reason: "response is not an object" };
  }
  const root = raw as Record<string, unknown>;
  if (!("data" in root)) {
    // A missing top-level `data` key is response key-set drift (docs/14 alert).
    return { ok: false, reason: "missing top-level 'data' key" };
  }
  const data = root.data as Record<string, unknown> | null;
  const product =
    data && typeof data === "object" ? (data.product as Record<string, unknown>) : undefined;
  if (!product || typeof product !== "object") {
    return { ok: false, reason: "missing 'data.product'" };
  }

  const nativeProductId = asInt(product.id);
  if (nativeProductId === null) {
    return { ok: false, reason: "missing or non-integer product.id" };
  }

  const uri = readUri(product.url);
  const canonicalUrl = uri ? canonicalProductUrl(uri) : null;
  if (!canonicalUrl) {
    return { ok: false, reason: "missing or unparseable product.url.uri" };
  }

  const title = typeof product.title_fa === "string" ? normalizePersian(product.title_fa) : "";

  const availability = mapAvailability(product.status);
  const variants = Array.isArray(product.variants) ? product.variants : [];
  // Prefer the explicit default variant; otherwise the first variant. An empty
  // variants list is VALID for an unavailable product (docs/10 step 3) — no offer,
  // no invented price.
  const chosen = pickVariant(product.default_variant, variants);
  const offer = chosen ? parseOffer(chosen) : null;

  // Contradiction guard (docs/10 failure recovery): a marketable status with no
  // variants is a validation/drift signal, not something to coerce.
  if (availability === "in_stock" && offer === null) {
    return { ok: false, reason: "marketable product with no variants (drift)" };
  }

  return {
    ok: true,
    product: {
      nativeProductId,
      canonicalUrl,
      title,
      availability: offer === null ? unavailableStatus(availability) : availability,
      offer,
      parserVersion: PARSER_VERSION,
    },
  };
}

function unavailableStatus(status: AvailabilityStatus): AvailabilityStatus {
  return status === "in_stock" ? "unavailable" : status;
}

function pickVariant(defaultVariant: unknown, variants: unknown[]): Record<string, unknown> | null {
  if (defaultVariant && typeof defaultVariant === "object" && "id" in (defaultVariant as object)) {
    return defaultVariant as Record<string, unknown>;
  }
  const first = variants[0];
  if (first && typeof first === "object" && "id" in (first as object)) {
    return first as Record<string, unknown>;
  }
  return null;
}

function parseOffer(variant: Record<string, unknown>): ParsedOffer | null {
  const nativeVariantId = asInt(variant.id);
  if (nativeVariantId === null) return null;
  const offer: ParsedOffer = { nativeVariantId };

  const seller = variant.seller as Record<string, unknown> | undefined;
  if (seller && seller.id !== undefined && seller.id !== null) {
    offer.nativeSellerId = String(seller.id);
  }

  const price = variant.price as Record<string, unknown> | undefined;
  if (price && typeof price === "object") {
    const selling = asInt(price.selling_price);
    if (selling !== null) offer.price = rawRial(selling);
    const rrp = asInt(price.rrp_price);
    if (rrp !== null) offer.listPrice = rawRial(rrp);
    const stock = asInt(price.marketable_stock);
    if (stock !== null) offer.stockSignal = stock;
  }
  return offer;
}

function rawRial(amount: number): RawAmount {
  const value = String(Math.trunc(amount));
  return { text: `${value} ${RIAL_UNIT}`, value, unit: RIAL_UNIT };
}

// mapAvailability maps DK status tokens (docs/11: map `out_of_stock` and the
// `ناموجود` badge together; never infer availability from price).
function mapAvailability(status: unknown): AvailabilityStatus {
  const s = typeof status === "string" ? status.toLowerCase() : "";
  switch (s) {
    case "marketable":
      return "in_stock";
    case "out_of_stock":
      return "out_of_stock";
    case "limited":
      return "limited";
    default:
      return "unavailable";
  }
}

function readUri(url: unknown): string | null {
  if (url && typeof url === "object" && "uri" in (url as object)) {
    const uri = (url as Record<string, unknown>).uri;
    return typeof uri === "string" ? uri : null;
  }
  return typeof url === "string" ? url : null;
}

function asInt(v: unknown): number | null {
  if (typeof v === "number" && Number.isFinite(v)) return Math.trunc(v);
  if (typeof v === "string" && /^-?\d+$/.test(v.trim())) return Number.parseInt(v, 10);
  return null;
}
