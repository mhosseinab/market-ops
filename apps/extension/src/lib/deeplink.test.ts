import { describe, expect, it } from "vitest";
import { buildDeepLink } from "./deeplink";

describe("deep links — EXT-008 correct context chip, real user-clicked hrefs", () => {
  it("product deep link matches apps/web navConfig's /product?variantId= contract", () => {
    expect(buildDeepLink({ kind: "product", id: "v-1" })).toMatch(/\/product\?variantId=v-1$/);
  });

  it("event deep link matches /event?eventId=", () => {
    expect(buildDeepLink({ kind: "event", id: "e-1" })).toMatch(/\/event\?eventId=e-1$/);
  });

  it("chat deep link lands on the correct product context (chat-dock auto-open is a named follow-up)", () => {
    expect(buildDeepLink({ kind: "chat", id: "v-1" })).toMatch(/\/product\?variantId=v-1&chat=1$/);
  });

  it("URL-encodes the id", () => {
    expect(buildDeepLink({ kind: "product", id: "a b" })).toContain("variantId=a%20b");
  });
});
