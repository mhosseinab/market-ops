// Privacy boundary (docs/12, PRD §12). NOTHING leaves the extension until it is
// allow-listed and redacted. This module is the single redaction choke point:
//   - review `user_name` and question `sender` are UNCONDITIONALLY stripped,
//     regardless of any anonymity flag;
//   - any field name matching /cookie|auth|token|session/i is dropped (diagnostic
//     capture redaction);
//   - session-adjacent fields (address, cart, cookies, tokens) are never retained;
//   - unexpected name-like fields are dropped by default.
// Marketplace text is INERT data — never interpreted as instructions.

// Field-name patterns that must never survive redaction.
const SECRET_KEY_RE = /cookie|auth|token|session/i;
const NAME_LIKE_RE =
  /(^|_)(user_?name|sender|name|full_?name|first_?name|last_?name|phone|mobile|email|address)($|_)/i;
const SESSION_ADJACENT = new Set([
  "cart",
  "address",
  "addresses",
  "cookie",
  "cookies",
  "authorization",
  "token",
  "tokens",
  "session",
  "user",
  "profile",
]);

// redactValue deep-clones a JSON value, dropping every key that is a secret,
// session-adjacent, or name-like. Arrays are redacted element-wise. Primitives
// pass through unchanged (they are inert data).
export function redactValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return value.map(redactValue);
  }
  if (value !== null && typeof value === "object") {
    const out: Record<string, unknown> = {};
    for (const [key, v] of Object.entries(value as Record<string, unknown>)) {
      if (SECRET_KEY_RE.test(key)) continue;
      if (SESSION_ADJACENT.has(key.toLowerCase())) continue;
      if (NAME_LIKE_RE.test(key)) continue;
      out[key] = redactValue(v);
    }
    return out;
  }
  return value;
}

// containsSecretKey reports whether a value (recursively) still carries any
// secret/name-like/session-adjacent key. Used by the storage audit to prove no
// forbidden field ever reaches storage or the wire (fail closed).
export function containsSecretKey(value: unknown): boolean {
  if (Array.isArray(value)) {
    return value.some(containsSecretKey);
  }
  if (value !== null && typeof value === "object") {
    for (const [key, v] of Object.entries(value as Record<string, unknown>)) {
      if (SECRET_KEY_RE.test(key)) return true;
      if (SESSION_ADJACENT.has(key.toLowerCase())) return true;
      if (NAME_LIKE_RE.test(key)) return true;
      if (containsSecretKey(v)) return true;
    }
  }
  return false;
}
