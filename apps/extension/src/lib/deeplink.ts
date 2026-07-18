// Deep links (EXT-008: deep-link to product, events, and contextual chat, with
// a correct context chip). These are NORMAL hrefs a human clicks to open the
// SPA in a new tab — never an automated navigation of the current page
// (EXT-010 boundary is about the CONTENT SCRIPT's effect on digikala.com; the
// popup/overlay opening the market-ops web app in a new tab on user click is
// ordinary browser navigation, not extension automation).
//
// Paths mirror apps/web/src/app/navConfig.ts EXACTLY (`/product?variantId=`,
// `/event?eventId=`) — this module owns no route knowledge of its own.

export type DeepLinkKind = "product" | "event" | "chat";

export interface DeepLinkTarget {
  readonly kind: DeepLinkKind;
  /** The gateway-domain id the target screen's search param expects. */
  readonly id: string;
}

// The web app's origin, injected at build time — same pattern as
// VITE_GATEWAY_BASE_URL (gateway.ts).
const WEB_BASE_URL = import.meta.env.VITE_WEB_BASE_URL ?? "http://localhost:5173";

// buildDeepLink returns the exact URL for a product or event deep link. For
// "chat" it links to the same screen with a `chat=1` context hint — the web
// app does not yet consume this param to auto-open the dock (a genuine gap:
// chat-dock open state is local React state, not URL-driven, as of S29); this
// is named here rather than silently dropped, and the link still lands the
// user on the CORRECT context (product/event), which is the EXT-008
// acceptance bar ("correct context chip").
export function buildDeepLink(target: DeepLinkTarget): string {
  switch (target.kind) {
    case "product":
      return `${WEB_BASE_URL}/product?variantId=${encodeURIComponent(target.id)}`;
    case "event":
      return `${WEB_BASE_URL}/event?eventId=${encodeURIComponent(target.id)}`;
    case "chat":
      return `${WEB_BASE_URL}/product?variantId=${encodeURIComponent(target.id)}&chat=1`;
  }
}
