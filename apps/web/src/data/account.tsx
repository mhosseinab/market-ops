import { createContext, type ReactNode, useContext, useMemo } from "react";

// Active marketplace-account context. The gateway contract carries no
// list-accounts endpoint in P0, so the active account id is supplied as
// configuration (env for a real deploy, the deterministic dev seed otherwise)
// and threaded to every account-scoped query. Tests override it via the prop.
// This is FE state only — it never recomputes or infers anything the core owns.

const SEED_ACCOUNT_ID = "00000000-0000-0000-0000-000000000003";

interface AccountState {
  readonly marketplaceAccountId: string;
}

const AccountContext = createContext<AccountState | null>(null);

export function AccountProvider({
  children,
  marketplaceAccountId,
}: {
  children: ReactNode;
  marketplaceAccountId?: string;
}) {
  const value = useMemo<AccountState>(
    () => ({
      marketplaceAccountId:
        marketplaceAccountId ??
        (import.meta.env.VITE_MARKETPLACE_ACCOUNT_ID as string | undefined) ??
        SEED_ACCOUNT_ID,
    }),
    [marketplaceAccountId],
  );
  return <AccountContext.Provider value={value}>{children}</AccountContext.Provider>;
}

export function useAccount(): AccountState {
  const ctx = useContext(AccountContext);
  if (!ctx) throw new Error("useAccount must be used within AccountProvider");
  return ctx;
}
