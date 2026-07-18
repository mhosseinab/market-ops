import { describe, expect, it } from "vitest";
import { deriveChatContext } from "./context";

// CHAT-001: contextual entry from product/event/recommendation/action BINDS that
// context deterministically from the route + deep-link search — never guessed.
describe("deriveChatContext", () => {
  it("binds the product context (and entity) on the product/cost routes", () => {
    expect(deriveChatContext("/product", { variantId: "v1" })).toEqual({
      kind: "product",
      entityId: "v1",
    });
    expect(deriveChatContext("/cost", { variantId: "v2" })).toEqual({
      kind: "product",
      entityId: "v2",
    });
    expect(deriveChatContext("/product", {})).toEqual({ kind: "product" });
  });

  it("binds event / recommendation / action contexts to their deep-link entity", () => {
    expect(deriveChatContext("/event", { eventId: "e1" })).toEqual({
      kind: "event",
      entityId: "e1",
    });
    expect(deriveChatContext("/recommendation", { cardId: "c1" })).toEqual({
      kind: "recommendation",
      entityId: "c1",
    });
    expect(deriveChatContext("/actions", { actionId: "a1" })).toEqual({
      kind: "action",
      entityId: "a1",
    });
  });

  it("maps bulk / settings / operations and falls back to the global account", () => {
    expect(deriveChatContext("/bulk", {}).kind).toBe("bulk");
    expect(deriveChatContext("/settings", {}).kind).toBe("settings");
    expect(deriveChatContext("/operations", {}).kind).toBe("operations");
    expect(deriveChatContext("/today", {}).kind).toBe("global");
    expect(deriveChatContext("/", {}).kind).toBe("global");
  });
});
