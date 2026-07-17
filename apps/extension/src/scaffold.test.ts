import { expect, test } from "vitest";
import { extensionName } from "./index";

test("extension scaffold placeholder is wired", () => {
  expect(extensionName).toBe("market-ops-extension");
});
