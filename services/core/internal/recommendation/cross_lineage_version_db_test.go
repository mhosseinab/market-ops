package recommendation_test

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mhosseinab/market-ops/services/core/internal/recommendation"
)

// BULK-PROTOCOL DESIGN RECORD (d) — CROSS-LINEAGE CANDIDATE ORDERING (issue #87,
// prior finding 6).
//
// The rule, verbatim: version counters are monotonic ONLY WITHIN one lineage. Version
// numbers from different lineages are NOT comparable and must never be ordered, ranged,
// or diffed against one another; and a bare version is never accepted without its
// lineage, because THE BINDING IS THE PAIR. "v3" is meaningful only as
// "(lineage L, version 3)".
//
// This is a PROPERTY test, not an example test: the PRD calls for property shapes on
// approval versioning. It generates unrelated lineages with deliberately overlapping
// and deliberately skewed version counters — including a decoy lineage whose counter is
// numerically LARGER than the subject's — and asserts that the validity of a binding
// depends ONLY on its own lineage's counter.
//
// If a future change reintroduces a comparison across lineages (an ORDER BY, a MAX, a
// range, or a bare-version lookup), the decoy's larger counter is what makes it fail
// here instead of in production.
func TestSelectionVersions_AreNeverComparedAcrossLineages(t *testing.T) {
	pool, q := newPool(t)
	ctx := context.Background()
	svc := recommendation.NewService(pool)
	account, variant := seedVariant(t, q)
	card := awaitingCard(t, svc, account, variant)
	member := []recommendation.PreviewMemberInput{{VariantID: variant, RecommendationID: card.RecommendationID}}

	// mintLineage mints `n` successive versions in a FRESH lineage and returns the
	// lineage plus its own current (greatest) version.
	mintLineage := func(n int) (uuid.UUID, int32) {
		t.Helper()
		lineage := uuid.Nil
		var version int32
		for range n {
			res, err := svc.PreviewBulkSelection(ctx, account, lineage, "cross-lineage", nil, member)
			if err != nil {
				t.Fatalf("preview: %v", err)
			}
			lineage, version = res.Set.LineageID, res.Set.Version
		}
		return lineage, version
	}

	seed := time.Now().UnixNano()
	rng := rand.New(rand.NewPCG(uint64(seed), 0x87))
	t.Logf("property seed = %d", seed)

	for range 12 {
		subjectDepth := 1 + rng.IntN(4)
		// The DECOY is deliberately allowed to run AHEAD of the subject, so any
		// cross-lineage MAX/ORDER BY/range would pick it and change the answer.
		decoyDepth := subjectDepth + 1 + rng.IntN(4)

		subject, subjectCurrent := mintLineage(subjectDepth)
		decoy, decoyCurrent := mintLineage(decoyDepth)
		if decoyCurrent <= subjectCurrent {
			t.Fatalf("decoy lineage did not run ahead (%d <= %d); the property would be vacuous", decoyCurrent, subjectCurrent)
		}

		// PROPERTY 1: a binding to the subject's OWN current version is valid, no
		// matter how far ahead any unrelated lineage has run.
		ok, err := svc.BulkPreviewValid(ctx, subject, subjectCurrent)
		if err != nil {
			t.Fatalf("validate subject binding: %v", err)
		}
		if !ok {
			t.Fatalf("(lineage %s, version %d) reported INVALID while an unrelated lineage sat at version %d; "+
				"versions were compared across lineages", subject, subjectCurrent, decoyCurrent)
		}

		// PROPERTY 2: the decoy's version number, presented against the SUBJECT's
		// lineage, is not a valid binding. A bare version carries no authority — the
		// binding is the PAIR.
		if decoyCurrent != subjectCurrent {
			ok, err := svc.BulkPreviewValid(ctx, subject, decoyCurrent)
			if err != nil {
				t.Fatalf("validate borrowed version: %v", err)
			}
			if ok {
				t.Fatalf("version %d borrowed from lineage %s validated against lineage %s; "+
					"a bare version was accepted without its lineage", decoyCurrent, decoy, subject)
			}
		}

		// PROPERTY 3: every SUPERSEDED version of the subject's own lineage is
		// invalid — monotonicity still holds WITHIN a lineage, so this test cannot be
		// passed by a validator that simply says "no".
		for v := int32(1); v < subjectCurrent; v++ {
			ok, err := svc.BulkPreviewValid(ctx, subject, v)
			if err != nil {
				t.Fatalf("validate superseded version: %v", err)
			}
			if ok {
				t.Fatalf("superseded (lineage %s, version %d) validated; within-lineage monotonicity broken", subject, v)
			}
		}
	}
}
