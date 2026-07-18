// Command marginreconcile is the Gate 0a margin-reconciliation harness. It runs
// the deterministic contribution engine (internal/margin, PRD §9.2) over the
// committed synthetic settlement examples and reports, per example, whether the
// engine reproduces the expected contribution within the example's declared
// rounding tolerance.
//
// Five SYNTHETIC examples ship today; the real ≥30 representative settlement
// examples arrive with S35 — a GATED step (live/paid measurement) that is never
// run unattended. This CLI is the reusable comparison the S35 examples will feed.
// It exits non-zero if any example mismatches, so it can gate a build.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mhosseinab/market-ops/services/core/internal/margin"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	examples, err := margin.LoadSettlementExamples()
	if err != nil {
		return err
	}
	results := margin.Reconcile(examples)

	p := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }

	failures := 0
	p("margin reconciliation — %d synthetic settlement example(s), rounding rule %s\n",
		len(results), margin.ContributionRoundingRule)
	for _, r := range results {
		switch {
		case r.Err != nil:
			failures++
			p("  FAIL  %-42s error: %v\n", r.Name, r.Err)
		case !r.Matched:
			failures++
			p("  FAIL  %-42s engine=%s expected=%s delta=%d\n",
				r.Name, r.Got.String(), r.Expected.String(), r.DeltaMantissa)
		default:
			p("  OK    %-42s %s\n", r.Name, r.Got.String())
		}
	}
	if failures > 0 {
		return fmt.Errorf("margin reconciliation FAILED: %d of %d example(s) mismatched", failures, len(results))
	}
	p("all %d example(s) reconciled within declared tolerance\n", len(results))
	return nil
}
