import type { components } from "@market-ops/gen-ts";

// The allow-listed capture-upload wire shape is OWNED by the gateway contract
// (contracts/gateway.openapi.yaml). The extension consumes it read-only; a shape
// mismatch is escalated to api_data_contracts, never patched here.
export type CaptureUpload = components["schemas"]["CaptureUpload"];
export type PairingCredential = components["schemas"]["PairingCredential"];
export type RawAmount = components["schemas"]["RawAmount"];
// The credential-scoped owned-target read (#145, GET /ext/owned-targets) returns
// the account's ObservationTargetList; the extension consumes it read-only and
// projects only the fields the EXT-004 gate needs.
export type ObservationTargetList = components["schemas"]["ObservationTargetList"];
export type AvailabilityStatus = CaptureUpload["availabilityStatus"];

// ParsedOffer is the single current offer extracted from the product-detail API
// (docs/06: the API is the source of truth for variant price/seller). Money is
// RAW evidence only — the extension never derives a Money.
export interface ParsedOffer {
  nativeVariantId: number;
  nativeSellerId?: string;
  price?: RawAmount;
  listPrice?: RawAmount;
  stockSignal?: number;
}

// ParsedProduct is the normalized, redacted result of capturing one product page.
// It carries NO reviewer/question identity (redacted at parse) and no session
// data. `offer` is null for an unavailable product — a price is never invented
// (docs/10 step 3).
export interface ParsedProduct {
  nativeProductId: number;
  canonicalUrl: string;
  title: string;
  availability: AvailabilityStatus;
  offer: ParsedOffer | null;
  parserVersion: string;
}

// ParseResult is a discriminated result: either a parsed product or a structured
// parser-drift reason (§10.4). The extension never throws raw across the seam.
export type ParseResult = { ok: true; product: ParsedProduct } | { ok: false; reason: string };
