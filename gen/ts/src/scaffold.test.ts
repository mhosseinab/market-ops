import { expect, test } from "vitest";
import { generated } from "./index";

test("gen/ts placeholder is wired before S4 regenerates it", () => {
  expect(generated).toBe(false);
});
