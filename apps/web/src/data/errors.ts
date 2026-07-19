// Structured gateway error carried across the TanStack mutation boundary. The
// generated client returns the §8 ErrorEnvelope ({ code, message, detail?,
// requestId? }) as `result.error`; `unwrap` (see hooks.ts) reifies it into this
// class so a screen can render an ACTIONABLE, LOCALIZED failure surface instead
// of a silent fallback. `message`/`detail` are diagnostic free text and NEVER
// user-facing copy (free-text containment): the UI localizes on `status`/`code`
// and shows only the machine `requestId` (LTR-isolated) for correlation.

export type ErrorEnvelope = {
  code?: string;
  message?: string;
  detail?: string;
  requestId?: string;
};

// Classes map the HTTP status to a stable, localizable bucket. The screen never
// branches on a raw status number; it renders the class's catalog key.
export type ErrorClass =
  | "badRequest"
  | "unauthorized"
  | "forbidden"
  | "conflict"
  | "server"
  | "generic";

export class GatewayError extends Error {
  readonly code?: string;
  readonly status?: number;
  readonly requestId?: string;

  constructor(env: ErrorEnvelope, status?: number) {
    super(env.message ?? env.code ?? "request_failed");
    this.name = "GatewayError";
    this.code = env.code;
    this.status = status;
    this.requestId = env.requestId;
  }
}

export function classifyStatus(status?: number): ErrorClass {
  switch (status) {
    case 400:
      return "badRequest";
    case 401:
      return "unauthorized";
    case 403:
      return "forbidden";
    case 409:
      return "conflict";
    default:
      return status !== undefined && status >= 500 ? "server" : "generic";
  }
}

export function asGatewayError(error: unknown): GatewayError | null {
  return error instanceof GatewayError ? error : null;
}
