import type { MessageKey } from "@market-ops/locale";
import { useRouter, useRouterState } from "@tanstack/react-router";
import { type FormEvent, useState } from "react";
import { useT } from "../app/i18n";
import { asGatewayError } from "../data/errors";
import { useLogin } from "../data/hooks";

// Production login screen (issue #168). Persian-first; every string flows through
// the catalog (LOC-002). The session is opened server-side and delivered ONLY in
// the secure httpOnly cookie — this screen NEVER reads, stores, or logs a token
// or password (no localStorage/sessionStorage/IndexedDB, no client-readable
// cookie). Credentials live only in React state while typing and in the single
// POST /auth/login body, then are discarded.
//
// Credentials are OPAQUE and never digit-normalized: normalizing a password would
// corrupt the secret bytes, and an email is a technical identifier, so both are
// LTR-isolated inputs sent verbatim (the digit-normalization boundary applies to
// NUMERIC inputs, not credentials).

// Only accept an INTERNAL, absolute path as the post-login destination — never an
// attacker-supplied absolute URL or protocol-relative `//host` (open-redirect
// containment). Anything else falls back to the default landing route.
function safeRedirect(raw: string | undefined): string {
  if (typeof raw !== "string") return "/today";
  if (!raw.startsWith("/") || raw.startsWith("//") || raw.startsWith("/\\")) return "/today";
  return raw;
}

// A login failure maps to a NON-enumerating catalog key: a 401 says only that the
// email/password pair is wrong (never which field), every other failure is the
// generic retry copy. The server's diagnostic message is never rendered.
function loginErrorKey(error: unknown): MessageKey {
  const gatewayError = asGatewayError(error);
  return gatewayError?.status === 401
    ? "auth.login.error.invalidCredentials"
    : "auth.login.error.generic";
}

export function Login() {
  const t = useT();
  const router = useRouter();
  const login = useLogin();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  // The preserved destination the auth gate stashed when it bounced an
  // unauthenticated request here (falls back to the default landing route).
  const redirect = useRouterState({
    select: (s) => (s.location.search as { redirect?: string }).redirect,
  });
  const expired = redirect !== undefined;

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    login.mutate(
      { email, password },
      {
        onSuccess: () => {
          void router.navigate({ to: safeRedirect(redirect) } as never);
        },
      },
    );
  };

  const canSubmit = email.trim() !== "" && password !== "" && !login.isPending;

  return (
    <main className="auth">
      <form className="auth__card" onSubmit={onSubmit} aria-label={t("auth.login.title")}>
        <h1 className="auth__title">{t("auth.login.title")}</h1>
        <p className="auth__subtitle">{t("auth.login.subtitle")}</p>

        {/* When the gate bounced an already-signed-in user here, it left a redirect
            target — surface the session-ended note so the re-auth is explained. */}
        {expired ? (
          <p className="auth__note" role="status" data-testid="login-expired-note">
            {t("auth.login.expiredNote")}
          </p>
        ) : null}

        <label className="field">
          <span className="field__label">{t("auth.login.emailLabel")}</span>
          <input
            type="email"
            autoComplete="username"
            className="field__input ltr"
            data-testid="login-email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </label>

        <label className="field">
          <span className="field__label">{t("auth.login.passwordLabel")}</span>
          <input
            type="password"
            autoComplete="current-password"
            className="field__input ltr"
            data-testid="login-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </label>

        {login.isError ? (
          <p className="blocker-note" role="alert" data-testid="login-error">
            {t(loginErrorKey(login.error))}
          </p>
        ) : null}

        <button
          type="submit"
          className="btn btn--primary auth__submit"
          data-testid="login-submit"
          disabled={!canSubmit}
        >
          {t(login.isPending ? "auth.login.submitting" : "auth.login.submit")}
        </button>
      </form>
    </main>
  );
}

// The auth-gate loading state (STATE_MATRIX): rendered while the authed layout's
// `beforeLoad` resolves GET /auth/me, before any protected screen mounts. The
// `<output>` carries an implicit live region so the check is announced.
export function AuthGateLoading() {
  const t = useT();
  return (
    <main className="auth">
      <output className="auth__loading" aria-label={t("auth.gate.loading")}>
        {t("auth.gate.loading")}
      </output>
    </main>
  );
}
