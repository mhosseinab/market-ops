// Command seede2e provisions ONE owner user with a KNOWN password for the S32
// system-test suites (kill-switch Playwright journeys, the adversarial
// containment replay) that must authenticate against a REAL running gateway.
// It is test/CI-only infrastructure — it is never invoked by
// deploy/compose.prod.yml or any production path, and it never runs unless
// explicitly invoked (task test:integration / the CI integration job).
//
// It targets the SAME deterministic dev-fixture user `task db:reset` already
// seeds (services/core/fixtures/dev_seed.sql: owner@dev.local, organization
// 00000000-0000-0000-0000-000000000001, marketplace account
// 00000000-0000-0000-0000-000000000003) by default. That fixed account id is
// also what the web app's AccountProvider defaults to
// (apps/web/src/data/account.tsx SEED_ACCOUNT_ID) absent an explicit
// VITE_MARKETPLACE_ACCOUNT_ID — reusing it means the Playwright kill-switch
// journey and the adversarial replay script need no frontend/env
// reconfiguration to see the same account. Only that fixture row is missing a
// password (dev_seed.sql never ships one); this command is the ONLY thing
// that sets it, and only for test/CI runs.
//
// It is idempotent: re-running against the same DATABASE_URL only sets a
// password on an EXISTING user (found by email; it creates a user/org only if
// the dev-fixture row is genuinely absent), so a flaky CI re-run never
// diverges from a known state and never mints duplicate accounts.
//
//	SEEDE2E_EMAIL     (default owner@dev.local — the dev_seed fixture user)
//	SEEDE2E_PASSWORD  (required — no default; a missing password fails closed)
//	DATABASE_URL      (required)
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seede2e:", err)
		os.Exit(1)
	}
}

func run() error {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	email := os.Getenv("SEEDE2E_EMAIL")
	if email == "" {
		email = "owner@dev.local"
	}
	// Canonicalize the identifier exactly as the login/write paths do (issue #12),
	// so the lookup on lower(email) resolves and a created user is stored in the
	// same normalized form.
	email = normalize.Email(email)
	password := os.Getenv("SEEDE2E_PASSWORD")
	if password == "" {
		// Fail closed: never provision a credential-less or guessable-default
		// account, even for test infrastructure.
		return fmt.Errorf("SEEDE2E_PASSWORD is required (no default)")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()
	q := db.New(pool)

	user, err := q.GetUserByEmail(ctx, email)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("look up user %s: %w", email, err)
		}
		// The dev-fixture row is genuinely absent (task db:reset was not run
		// against this database) — fall back to minting a fresh org/user so the
		// command still succeeds standalone, but this path does NOT reuse the
		// frontend's hardcoded SEED_ACCOUNT_ID; callers relying on the fixed
		// account id must run `task db:reset` first.
		org, orgErr := q.CreateOrganization(ctx, "S32 System Test Org")
		if orgErr != nil {
			return fmt.Errorf("create organization: %w", orgErr)
		}
		user, err = q.CreateUser(ctx, db.CreateUserParams{
			OrganizationID: org.ID, Email: email, Role: "owner",
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}
	}

	authSvc := auth.NewService(q)
	if err := authSvc.SetPassword(ctx, user.ID, password); err != nil {
		return fmt.Errorf("set password: %w", err)
	}

	fmt.Printf("seede2e: password set for %s (user=%s org=%s)\n", email, user.ID, user.OrganizationID)
	return nil
}
