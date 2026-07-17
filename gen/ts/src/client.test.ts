import { expect, test } from "vitest";
import { createGatewayClient } from "./index";

test("gen/ts exposes a typed gateway client bound to the generated schema", () => {
  const client = createGatewayClient({ baseUrl: "http://localhost:8080" });
  expect(typeof client.GET).toBe("function");
});
