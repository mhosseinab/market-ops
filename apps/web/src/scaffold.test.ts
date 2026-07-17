import { expect, test } from "vitest";
import { appName } from "./index";

test("web scaffold placeholder is wired", () => {
  expect(appName).toBe("market-ops-web");
});
