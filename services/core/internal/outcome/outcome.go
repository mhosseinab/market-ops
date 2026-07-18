// Package outcome implements OUT-001 and the §15.3 outcome rule: every reconciled
// action opens a seven-day outcome window, and once the window closes the result
// and confidence are computed once (or the window is marked Not Measurable when
// the required evidence is absent).
//
// The evaluation is PURE (no I/O, no clock beyond the passed instant) so the
// §15.3 rule table is provable in isolation; persistence of the append-only
// windows and their computed results lives in the DB service.
package outcome

import "time"

// window is the fixed seven-day span (OUT-001).
const window = 7 * 24 * time.Hour

// Result is the §15.3 outcome classification of a closed window. The set is
// closed; NotMeasurable is the fail-closed value when required evidence is absent.
type Result string

const (
	// Positive — the objective metric improved without a floor breach.
	Positive Result = "positive"
	// Negative — the objective metric worsened or contribution breached its bound.
	Negative Result = "negative"
	// Neutral — the change stayed inside configured materiality.
	Neutral Result = "neutral"
	// Inconclusive — concurrent changes prevent directional attribution.
	Inconclusive Result = "inconclusive"
	// NotMeasurable — required outcome evidence is absent (fail-closed).
	NotMeasurable Result = "not_measurable"
)

// Valid reports whether r is a known result.
func (r Result) Valid() bool {
	switch r {
	case Positive, Negative, Neutral, Inconclusive, NotMeasurable:
		return true
	default:
		return false
	}
}

// Confidence is the §15.3 confidence grade, driven purely by how many material
// concurrent changes overlapped the window.
type Confidence string

const (
	// High — no material concurrent change.
	High Confidence = "high"
	// Medium — exactly one material concurrent change.
	Medium Confidence = "medium"
	// Low — two or more material concurrent changes.
	Low Confidence = "low"
)

// Valid reports whether c is a known confidence grade.
func (c Confidence) Valid() bool {
	switch c {
	case High, Medium, Low:
		return true
	default:
		return false
	}
}

// Inputs is the fully-resolved evidence the §15.3 rule evaluates at window close.
// The engine that owns the metrics decides each boolean; this package only
// classifies. AttributionBlocked is set when concurrent changes make the
// direction unknowable (distinct from mere confidence dilution).
type Inputs struct {
	// EvidenceComplete — the required outcome evidence is present. False ⇒ the
	// result is NotMeasurable regardless of any other field.
	EvidenceComplete bool
	// AttributionBlocked — concurrent changes prevent directional attribution.
	AttributionBlocked bool
	// FloorBreached — a hard contribution floor was breached in the window.
	FloorBreached bool
	// ContributionBreachedBound — contribution breached its expected bound.
	ContributionBreachedBound bool
	// WithinMateriality — the objective-metric change stayed inside configured
	// materiality (⇒ Neutral).
	WithinMateriality bool
	// ObjectiveImproved / ObjectiveWorsened — the direction of the objective metric.
	ObjectiveImproved bool
	ObjectiveWorsened bool
	// ConcurrentMaterialChanges — the count of material concurrent changes,
	// driving the confidence grade.
	ConcurrentMaterialChanges int
}

// Evaluate applies the §15.3 rule and returns the result and confidence. The
// order is fixed and fail-closed: absent evidence is NotMeasurable; blocked
// attribution is Inconclusive; a breach is Negative (always material); an
// immaterial change is Neutral; then direction decides Positive/Negative.
func Evaluate(in Inputs) (Result, Confidence) {
	return classify(in), grade(in.ConcurrentMaterialChanges)
}

func classify(in Inputs) Result {
	switch {
	case !in.EvidenceComplete:
		return NotMeasurable
	case in.AttributionBlocked:
		return Inconclusive
	case in.FloorBreached || in.ContributionBreachedBound:
		// A floor/bound breach is always material and always Negative — it can
		// never be reported Positive (Positive requires no floor breach).
		return Negative
	case in.WithinMateriality:
		return Neutral
	case in.ObjectiveWorsened:
		return Negative
	case in.ObjectiveImproved:
		return Positive
	default:
		return Neutral
	}
}

func grade(concurrent int) Confidence {
	switch {
	case concurrent <= 0:
		return High
	case concurrent == 1:
		return Medium
	default:
		return Low
	}
}

// Window is a seven-day outcome window opened for a reconciled action (OUT-001).
// It is a value type; the append-only persistence lives in the DB service.
type Window struct {
	OpenedAt time.Time
	ClosesAt time.Time
}

// Open opens a seven-day window at openedAt (OUT-001).
func Open(openedAt time.Time) Window {
	return Window{OpenedAt: openedAt, ClosesAt: openedAt.Add(window)}
}

// Closed reports whether the window has closed at instant now (result/confidence
// are computed only once Closed is true).
func (w Window) Closed(now time.Time) bool {
	return !now.Before(w.ClosesAt)
}
