package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestAccountOwnershipDenialClassification pins the classifier that turns a raw
// database failure into the tenant denial (issue #412). It is a pure unit test, so
// it covers the DATABASE seam — migration 0048's composite foreign key — which the
// application predicate normally catches first and which is otherwise only
// reachable by racing an account-ownership change.
//
// Negative cases matter most: any error that is NOT this specific tenant violation
// must pass through untouched, so a genuine fault is never masked as a
// client-shaped denial.
func TestAccountOwnershipDenialClassification(t *testing.T) {
	account := uuid.New()

	cases := []struct {
		name      string
		err       error
		requested *uuid.UUID
		wantSeam  accountRejectionSeam
		wantOK    bool
	}{
		{
			name:      "org-scoped predicate matched no owned account",
			err:       pgx.ErrNoRows,
			requested: &account,
			wantSeam:  seamOrgScopedInsert,
			wantOK:    true,
		},
		{
			name:      "wrapped no-rows still classifies",
			err:       errors.Join(errors.New("create conversation"), pgx.ErrNoRows),
			requested: &account,
			wantSeam:  seamOrgScopedInsert,
			wantOK:    true,
		},
		{
			name:      "composite foreign key fired (predicate bypassed or raced)",
			err:       &pgconn.PgError{Code: foreignKeyViolation, ConstraintName: accountOwnershipConstraint},
			requested: &account,
			wantSeam:  seamCompositeForeignKey,
			wantOK:    true,
		},
		{
			// A different FK on the same table (author, organization) is a genuine
			// fault, not a tenant denial, and must NOT be laundered into a 404.
			name:      "a different foreign key violation is not a tenant denial",
			err:       &pgconn.PgError{Code: foreignKeyViolation, ConstraintName: "conversations_opened_by_user_id_fkey"},
			requested: &account,
			wantOK:    false,
		},
		{
			name:      "a non-FK database error is not a tenant denial",
			err:       &pgconn.PgError{Code: "40001", ConstraintName: accountOwnershipConstraint},
			requested: &account,
			wantOK:    false,
		},
		{
			// With NO account supplied the insert predicate is unconditionally true, so
			// a no-rows result is a genuine fault and must surface as one rather than be
			// swallowed into a tenant error nobody can act on.
			name:      "no account supplied: no-rows is a real fault, not a denial",
			err:       pgx.ErrNoRows,
			requested: nil,
			wantOK:    false,
		},
		{
			name:      "an unrelated error passes through",
			err:       context.DeadlineExceeded,
			requested: &account,
			wantOK:    false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seam, ok := accountOwnershipDenial(tc.err, tc.requested)
			if ok != tc.wantOK {
				t.Fatalf("denied = %v, want %v", ok, tc.wantOK)
			}
			if ok && seam != tc.wantSeam {
				t.Fatalf("seam = %q, want %q", seam, tc.wantSeam)
			}
		})
	}
}

// TestAccountDeniedIsADistinctSentinel keeps the denial from being conflated with
// the conversation-authorization denial: they have different causes and map to
// different responses, and a caller matching on the wrong one would mis-handle a
// tenant violation.
func TestAccountDeniedIsADistinctSentinel(t *testing.T) {
	if errors.Is(ErrAccountDenied, ErrConversationDenied) || errors.Is(ErrConversationDenied, ErrAccountDenied) {
		t.Fatal("ErrAccountDenied and ErrConversationDenied must be distinct sentinels")
	}
}
