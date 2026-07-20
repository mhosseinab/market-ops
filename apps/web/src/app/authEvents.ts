// Auth-lifecycle event bus (issue #168). The QueryClient's global error handler
// (see query.ts) detects an UNAUTHENTICATED (401) failure on any protected query
// and calls `notifyUnauthenticated()`. A React seam mounted inside the router
// (see AppShell) registers the handler that clears the protected cache and routes
// the browser to the login screen, preserving the intended destination.
//
// This module holds NO session token, password, or credential — only a callback
// and a re-entrancy guard. The session itself lives ONLY in the secure httpOnly
// cookie; the browser never reads or stores it.

type UnauthenticatedHandler = () => void;

let handler: UnauthenticatedHandler | null = null;

// STORM GUARD: a session expiry typically 401s several in-flight queries at once
// (Today feed, connector status, approval poll, …). Without a guard each 401
// would trigger its own navigation. `handling` collapses that burst into a
// SINGLE redirect; it is released only when a fresh session is established
// (`resetUnauthenticated`, called on a successful login), so a subsequent expiry
// redirects again.
let handling = false;

/** Register the redirect-to-login handler; returns an unregister cleanup. */
export function registerUnauthenticatedHandler(next: UnauthenticatedHandler): () => void {
  handler = next;
  return () => {
    if (handler === next) handler = null;
  };
}

/** Called by the query error boundary on a 401; collapses a burst to one redirect. */
export function notifyUnauthenticated(): void {
  if (handling) return;
  handling = true;
  handler?.();
}

/** Release the storm guard once a valid session is (re)established. */
export function resetUnauthenticated(): void {
  handling = false;
}
