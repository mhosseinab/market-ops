import { expect, test } from "vitest";
import { defaultLocale } from "./index";

test("locale scaffold placeholder is wired", () => {
  expect(defaultLocale).toBe("fa-IR");
});
