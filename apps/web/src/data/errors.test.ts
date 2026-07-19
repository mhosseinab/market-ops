import { describe, expect, it } from "vitest";
import { asGatewayError, classifyStatus, GatewayError } from "./errors";

describe("GatewayError (issue #82)", () => {
  it("carries status, machine code, and requestId from the envelope", () => {
    const err = new GatewayError(
      { code: "PERMISSION_DENIED", message: "no", requestId: "req-abc" },
      403,
    );
    expect(err.status).toBe(403);
    expect(err.code).toBe("PERMISSION_DENIED");
    expect(err.requestId).toBe("req-abc");
    expect(err).toBeInstanceOf(Error);
  });

  it("falls back to code then a stable string for the message", () => {
    expect(new GatewayError({ code: "X" }).message).toBe("X");
    expect(new GatewayError({}).message).toBe("request_failed");
  });

  it("classifyStatus maps each surfaced HTTP status to a stable class", () => {
    expect(classifyStatus(400)).toBe("badRequest");
    expect(classifyStatus(401)).toBe("unauthorized");
    expect(classifyStatus(403)).toBe("forbidden");
    expect(classifyStatus(409)).toBe("conflict");
    expect(classifyStatus(500)).toBe("server");
    expect(classifyStatus(503)).toBe("server");
    expect(classifyStatus(418)).toBe("generic");
    expect(classifyStatus(undefined)).toBe("generic");
  });

  it("asGatewayError narrows only real gateway errors", () => {
    expect(asGatewayError(new GatewayError({}, 400))).not.toBeNull();
    expect(asGatewayError(new Error("plain"))).toBeNull();
    expect(asGatewayError("nope")).toBeNull();
  });
});
