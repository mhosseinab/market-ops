package observation

import (
	"crypto/sha256"
	"encoding/hex"
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
