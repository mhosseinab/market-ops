package observation

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

// DedupKey is the deterministic idempotency key for a capture (OBS-008). It
// collapses a REPLAYED capture (identical target, offer identity, route, raw
// value/status, and capture instant) so a retry creates no duplicate current
// offer — while PRESERVING route provenance: the route is part of the key, so the
// SAME value observed by a DIFFERENT route is a distinct key (corroboration), and
// both routes are retained rather than one masking the other.
//
// The captured_at instant is included so two genuinely distinct captures (same
// value, different times) are distinct evidence; only a true replay — same
// instant, same everything — collides.
func DedupKey(c Capture) string {
	parts := []string{
		c.TargetID.String(),
		c.resolvedOfferIdentity(),
		string(c.Route),
		c.SubRoute,
		strings.TrimSpace(c.Price.Value),
		strings.TrimSpace(c.Price.Unit),
		strings.TrimSpace(c.ListPrice.Value),
		string(c.Availability),
		c.CapturedAt.UTC().Format(time.RFC3339Nano),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// EvidenceHash is the deterministic sha256 over the FULL evidence envelope — every
// capture-provided field that persists to the observation row (issue #44). Where
// DedupKey hashes only the collision SUBSET, this hashes the whole envelope, so two
// captures sharing a dedup key but differing in ANY material field (list-price
// unit/text, price text, stock signal, confidence, evidence/fixture refs, source
// url/type, parser/connector version, schema-valid flag, parsing warnings, native
// seller id, offer identity, captured instant) produce DIFFERENT hashes. The
// service compares this on a dedup-key conflict: equal ⇒ true replay (idempotent
// no-op); unequal ⇒ MATERIAL CONFLICT that must fail closed and be recorded, never
// silently discarded.
//
// Construction mirrors DedupKey: RAW tokens only — no parsing, no float, no money
// arithmetic (money quarantine). Fields are joined with a unit separator in a FIXED
// order (order-stable), and the variable-length parsing-warnings slice is length-
// prefixed and separated with a distinct byte so ["a","b"] and ["a\x1fb"] cannot
// collide.
func EvidenceHash(c Capture) string {
	parts := []string{
		c.TargetID.String(),
		c.resolvedOfferIdentity(),
		strconv.FormatInt(c.NativeVariantID, 10),
		c.NativeSellerID,
		string(c.Route),
		c.SubRoute,
		string(c.SourceType),
		c.SourceURL,
		c.ParserVersion,
		c.ConnectorVersion,
		c.EvidenceRef,
		c.RawFixtureRef,
		// Raw price evidence (money quarantine — verbatim tokens, never parsed).
		c.Price.Text, c.Price.Value, c.Price.Unit,
		c.ListPrice.Text, c.ListPrice.Value, c.ListPrice.Unit,
		string(c.Availability),
		stockSignalToken(c.StockSignal),
		string(c.Confidence),
		strconv.FormatBool(c.SchemaValid),
		c.CapturedAt.UTC().Format(time.RFC3339Nano),
		strconv.Itoa(len(c.ParsingWarnings)),
	}
	parts = append(parts, c.ParsingWarnings...)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(sum[:])
}

// stockSignalToken renders the optional stock signal as a stable token: a distinct
// sentinel for absent (nil), so "absent" and 0 never collide.
func stockSignalToken(v *int64) string {
	if v == nil {
		return "\x00nil"
	}
	return strconv.FormatInt(*v, 10)
}
