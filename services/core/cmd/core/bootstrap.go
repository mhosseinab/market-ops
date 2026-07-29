package main

// Subcommand `bootstrap-owner` — the production provisioning path that
// DEPLOYMENT.md §9 lists as required and that did not exist.
//
// Why this lives in cmd/core and not in its own binary: the production core
// image is distroless with a single binary and no shell (services/core/
// Dockerfile), so a second command can only be reached as an argument to that
// binary. `cmd/seede2e` is NOT that path — it is test/CI-only, defaults to the
// `owner@dev.local` fixture identity, hardcodes an S32 org name, never creates
// a marketplace account, runs outside a transaction, and is not compiled into
// any published image.
//
// What this does, in ONE transaction:
//
//	organizations       → the operator's organization
//	users               → one `owner` (PRD §15.1 role set)
//	user_credentials    → argon2id hash via auth.Service.SetPassword
//	marketplace_accounts→ the org's single DK account (organization_id is UNIQUE)
//
// It is idempotent and refuses to surprise anyone: if a user with that email
// already exists it reports the existing identifiers and changes NOTHING unless
// BOOTSTRAP_ROTATE_PASSWORD=true is set explicitly, in which case it rotates the
// credential AND revokes every live session for that user (a rotation that
// leaves old cookies valid is not a rotation).
//
// Inputs are environment variables, matching how every other secret already
// reaches this container:
//
//	DATABASE_URL                    required
//	BOOTSTRAP_OWNER_EMAIL           required
//	BOOTSTRAP_OWNER_PASSWORD        required, >= 12 characters, never logged
//	BOOTSTRAP_ORG_NAME              required when creating (ignored afterwards)
//	BOOTSTRAP_ACCOUNT_NATIVE_ID     required when creating the marketplace account
//	BOOTSTRAP_ACCOUNT_DISPLAY_NAME  optional, defaults to BOOTSTRAP_ORG_NAME
//	BOOTSTRAP_ROTATE_PASSWORD       optional, "true" to rotate an existing owner
//
// The plaintext password is read once, passed only to auth.HashPassword through
// auth.Service.SetPassword, and never written to a log line or an error string.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mhosseinab/market-ops/services/core/internal/auth"
	"github.com/mhosseinab/market-ops/services/core/internal/db"
	"github.com/mhosseinab/market-ops/services/core/internal/normalize"
	"github.com/mhosseinab/market-ops/services/core/internal/perm"
)

// minPasswordRunes is a fail-closed floor, not a policy. A production owner
// credential shorter than this is almost certainly a placeholder someone meant
// to replace.
const minPasswordRunes = 12

// runSubcommand dispatches `core <subcommand>`. Before this existed the binary
// ignored its arguments entirely; an unrecognized argument is now an error
// rather than a silently-booted server, so a typo in a deploy command fails
// loudly instead of starting a gateway nobody expected.
func runSubcommand(name string, args []string) error {
	switch name {
	case "bootstrap-owner":
		if len(args) != 0 {
			return fmt.Errorf("bootstrap-owner takes no arguments; configuration is read from the environment (got %v)", args)
		}
		return runBootstrapOwner(context.Background(), os.Getenv, os.Stdout)
	default:
		return fmt.Errorf("unknown subcommand %q (known: bootstrap-owner); run with no arguments to start the gateway", name)
	}
}

// bootstrapInput is the validated environment, separated from the work so the
// validation rules are testable without a database.
type bootstrapInput struct {
	databaseURL        string
	email              string
	password           string
	orgName            string
	accountNativeID    string
	accountDisplayName string
	rotate             bool
}

func loadBootstrapInput(getenv func(string) string) (bootstrapInput, error) {
	in := bootstrapInput{
		databaseURL:        strings.TrimSpace(getenv("DATABASE_URL")),
		email:              normalize.Email(getenv("BOOTSTRAP_OWNER_EMAIL")),
		password:           getenv("BOOTSTRAP_OWNER_PASSWORD"),
		orgName:            strings.TrimSpace(getenv("BOOTSTRAP_ORG_NAME")),
		accountNativeID:    strings.TrimSpace(getenv("BOOTSTRAP_ACCOUNT_NATIVE_ID")),
		accountDisplayName: strings.TrimSpace(getenv("BOOTSTRAP_ACCOUNT_DISPLAY_NAME")),
		rotate:             strings.EqualFold(strings.TrimSpace(getenv("BOOTSTRAP_ROTATE_PASSWORD")), "true"),
	}
	if in.databaseURL == "" {
		return bootstrapInput{}, errors.New("DATABASE_URL is required")
	}
	if in.email == "" {
		return bootstrapInput{}, errors.New("BOOTSTRAP_OWNER_EMAIL is required")
	}
	if !strings.Contains(in.email, "@") {
		return bootstrapInput{}, errors.New("BOOTSTRAP_OWNER_EMAIL does not look like an address")
	}
	if in.password == "" {
		return bootstrapInput{}, errors.New("BOOTSTRAP_OWNER_PASSWORD is required")
	}
	if len([]rune(in.password)) < minPasswordRunes {
		// The length is reported; the value never is.
		return bootstrapInput{}, fmt.Errorf("BOOTSTRAP_OWNER_PASSWORD must be at least %d characters", minPasswordRunes)
	}
	if in.accountDisplayName == "" {
		in.accountDisplayName = in.orgName
	}
	return in, nil
}

func runBootstrapOwner(ctx context.Context, getenv func(string) string, out io.Writer) error {
	in, err := loadBootstrapInput(getenv)
	if err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, in.databaseURL)
	if err != nil {
		return fmt.Errorf("bootstrap: connect: %w", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap: begin: %w", err)
	}
	// Rollback is a no-op after a successful Commit. Without this, a failure
	// between the organization insert and the user insert would leave an orphan
	// organization behind — the exact defect cmd/seede2e has.
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(pool).WithTx(tx)
	authSvc := auth.NewService(q)

	user, err := q.GetUserByEmail(ctx, in.email)
	switch {
	case err == nil:
		if !in.rotate {
			account, accErr := ensureAccount(ctx, q, user.OrganizationID, in)
			if accErr != nil {
				return accErr
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("bootstrap: commit: %w", err)
			}
			fmt.Fprintf(out, "bootstrap-owner: %s already exists; no credential change.\n", user.Email)
			printSummary(out, user, account)
			fmt.Fprintf(out, "bootstrap-owner: set BOOTSTRAP_ROTATE_PASSWORD=true to rotate this owner's password.\n")
			return nil
		}
		if err := authSvc.SetPassword(ctx, user.ID, in.password); err != nil {
			return fmt.Errorf("bootstrap: rotate credential: %w", err)
		}
		// A rotation that leaves existing session cookies valid has not revoked
		// anything. DeleteSessionsForUser is exactly the documented companion.
		if err := q.DeleteSessionsForUser(ctx, user.ID); err != nil {
			return fmt.Errorf("bootstrap: revoke sessions: %w", err)
		}

	case errors.Is(err, pgx.ErrNoRows):
		if in.orgName == "" {
			return errors.New("BOOTSTRAP_ORG_NAME is required when creating the first owner")
		}
		org, orgErr := q.CreateOrganization(ctx, in.orgName)
		if orgErr != nil {
			return fmt.Errorf("bootstrap: create organization: %w", orgErr)
		}
		user, err = q.CreateUser(ctx, db.CreateUserParams{
			OrganizationID: org.ID,
			Email:          in.email,
			Role:           string(perm.RoleOwner),
		})
		if err != nil {
			return fmt.Errorf("bootstrap: create owner: %w", err)
		}
		if err := authSvc.SetPassword(ctx, user.ID, in.password); err != nil {
			return fmt.Errorf("bootstrap: set credential: %w", err)
		}

	default:
		return fmt.Errorf("bootstrap: look up owner: %w", err)
	}

	account, err := ensureAccount(ctx, q, user.OrganizationID, in)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("bootstrap: commit: %w", err)
	}
	printSummary(out, user, account)
	return nil
}

// ensureAccount returns the organization's single marketplace account, creating
// it when absent. marketplace_accounts.organization_id is UNIQUE (the P0
// one-org-one-account invariant), so this is a get-or-create, never a list.
func ensureAccount(ctx context.Context, q *db.Queries, orgID uuid.UUID, in bootstrapInput) (db.MarketplaceAccount, error) {
	account, err := q.GetMarketplaceAccountByOrganization(ctx, orgID)
	if err == nil {
		return account, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return db.MarketplaceAccount{}, fmt.Errorf("bootstrap: look up marketplace account: %w", err)
	}
	if in.accountNativeID == "" {
		return db.MarketplaceAccount{}, errors.New("BOOTSTRAP_ACCOUNT_NATIVE_ID is required when creating the organization's marketplace account")
	}
	account, err = q.CreateMarketplaceAccount(ctx, db.CreateMarketplaceAccountParams{
		OrganizationID:  orgID,
		NativeAccountID: in.accountNativeID,
		DisplayName:     in.accountDisplayName,
	})
	if err != nil {
		return db.MarketplaceAccount{}, fmt.Errorf("bootstrap: create marketplace account: %w", err)
	}
	return account, nil
}

// printSummary emits the identifiers the release handoff record needs
// (DEPLOYMENT.md §12). No credential material is printed.
func printSummary(out io.Writer, user db.User, account db.MarketplaceAccount) {
	fmt.Fprintf(out, "bootstrap-owner: organization=%s\n", user.OrganizationID)
	fmt.Fprintf(out, "bootstrap-owner: user=%s email=%s role=%s\n", user.ID, user.Email, user.Role)
	fmt.Fprintf(out, "bootstrap-owner: marketplaceAccount=%s native=%s\n", account.ID, account.NativeAccountID)
}
