// Package recommendation assembles a PRC-001-complete recommendation from the
// event, margin (contribution), and policy engine outputs, and applies the
// PRC-002 blocker gate.
//
// Two never-cut invariants (PRD §4.6) govern this package:
//
//   - PRC-001 completeness: every recommendation field is EITHER present OR
//     explicitly unavailable WITH a reason. The Optional[T] type below makes that
//     structural — a field can never be silently empty.
//   - PRC-002 containment: any of the seven blockers (unconfirmed identity,
//     incomplete cost, ambiguous money unit, unusable evidence, unknown boundary,
//     permission failure, policy conflict) yields NO approval control. Approvable
//     returns false whenever a blocker is present; a negative fixture suite proves
//     zero controls across all seven.
//
// This package holds no float math: money values arrive already computed by the
// margin/policy engines (the only authoritative pricing source, §12.3). It is
// deliberately OUTSIDE the approval package so the version-bound control is minted
// only after Approvable is true.
package recommendation

// Optional carries a value that is EITHER present OR explicitly unavailable with
// a stated reason (PRC-001 "present or explicitly unavailable with a reason").
// The zero value is an unavailable field with an empty reason; always construct
// with Present or Unavailable so a reason accompanies every gap.
type Optional[T any] struct {
	value   T
	present bool
	reason  string
}

// Present wraps a known value.
func Present[T any](v T) Optional[T] {
	return Optional[T]{value: v, present: true}
}

// Unavailable marks a field as unavailable WITH a reason (never a silent empty).
func Unavailable[T any](reason string) Optional[T] {
	return Optional[T]{present: false, reason: reason}
}

// Get returns the value and whether it is present.
func (o Optional[T]) Get() (T, bool) { return o.value, o.present }

// IsPresent reports whether a value is present.
func (o Optional[T]) IsPresent() bool { return o.present }

// Reason returns the stated unavailability reason (empty when present).
func (o Optional[T]) Reason() string {
	if o.present {
		return ""
	}
	return o.reason
}
