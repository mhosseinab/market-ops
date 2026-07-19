package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
)

func main() {
	var (
		manifestPath = flag.String("manifest", "", "path to section16_manifest.json (default: <repo>/tools/integration/section16_manifest.json)")
		prdPath      = flag.String("prd", "", "path to PRD.md (default: <repo>/docs/PRD.md)")
		runAllPath   = flag.String("runall", "", "path to run_all.sh (default: <repo>/tools/integration/run_all.sh)")
		listOnly     = flag.Bool("list-only", false, "validate manifest + verify test existence only; skip the DB-backed go test run (for the no-DB Go CI job)")
	)
	flag.Parse()

	if err := run(*manifestPath, *prdPath, *runAllPath, *listOnly, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "section16gate: "+err.Error())
		os.Exit(1)
	}
}

func run(manifestPath, prdPath, runAllPath string, listOnly bool, out io.Writer) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	repoRoot, err := findRepoRoot(wd)
	if err != nil {
		return err
	}
	coreDir := findCoreDir(repoRoot, wd)

	if manifestPath == "" {
		manifestPath = repoRoot + "/tools/integration/section16_manifest.json"
	}
	if prdPath == "" {
		prdPath = repoRoot + "/docs/PRD.md"
	}
	if runAllPath == "" {
		runAllPath = repoRoot + "/tools/integration/run_all.sh"
	}

	m, err := loadManifest(manifestPath)
	if err != nil {
		return err
	}
	prd, err := os.ReadFile(prdPath) //nolint:gosec // gate-owned path.
	if err != nil {
		return fmt.Errorf("read PRD: %w", err)
	}
	canonical, err := parsePRDSection16(prd)
	if err != nil {
		return err
	}

	// 1. Structural completeness: every canonical §16 row is classified, no
	//    stale/duplicate rows, every classification carries its required fields.
	if problems := validateManifest(m, canonical); len(problems) > 0 {
		_, _ = fmt.Fprintf(out, "=== S32 §16 gate — manifest validation FAILED (%d problems) ===\n", len(problems))
		for _, p := range problems {
			_, _ = fmt.Fprintf(out, "  - %s\n", p.Error())
		}
		return fmt.Errorf("manifest does not fully classify PRD §16 (%d problems)", len(problems))
	}

	runAll, err := os.ReadFile(runAllPath) //nolint:gosec // gate-owned path.
	if err != nil {
		return fmt.Errorf("read run_all.sh: %w", err)
	}

	byEdge := make(map[string]*manifestRow, len(m.Rows))
	for i := range m.Rows {
		byEdge[m.Rows[i].EdgeCase] = &m.Rows[i]
	}

	var results []rowResult
	failed := 0
	for _, name := range canonical {
		row := byEdge[name]
		res := evaluateRow(row, coreDir, repoRoot, runAll, listOnly)
		results = append(results, res)
		if res.failed {
			failed++
		}
	}

	printReport(out, results, listOnly)
	if failed > 0 {
		return fmt.Errorf("%d of %d §16 rows failed the gate", failed, len(results))
	}
	return nil
}

type rowResult struct {
	edgeCase string
	class    string
	status   string // human-readable outcome
	failed   bool
}

func evaluateRow(row *manifestRow, coreDir, repoRoot string, runAll []byte, listOnly bool) rowResult {
	res := rowResult{edgeCase: row.EdgeCase, class: row.Classification}
	switch row.Classification {
	case classGoGate:
		evaluateGoGate(row, coreDir, listOnly, &res)
	case classOtherScenario:
		if scenarioPresent(runAll, row.Scenario) {
			res.status = "covered by run_all.sh scenario: " + row.Scenario
		} else {
			res.status = "MISSING scenario in run_all.sh: " + row.Scenario
			res.failed = true
		}
	case classOtherPlane:
		evaluateOtherPlane(row, repoRoot, &res)
	case classNonAutomatable:
		res.status = "non-automatable (owner " + row.Owner + "): " + row.Reason
	}
	return res
}

func evaluateGoGate(row *manifestRow, coreDir string, listOnly bool, res *rowResult) {
	byPkg := groupTestsByPackage(row.Tests)
	pkgs := sortedKeys(byPkg)
	total := len(row.Tests)

	// Existence check (no DB): a renamed/removed test or a zero-match selector fails here.
	var missing []string
	for _, pkg := range pkgs {
		miss, err := listExistingTests(coreDir, pkg, byPkg[pkg])
		if err != nil {
			res.status = "existence check error: " + err.Error()
			res.failed = true
			return
		}
		for _, m := range miss {
			missing = append(missing, pkg+"::"+m)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		res.status = fmt.Sprintf("MISSING/renamed tests (fail): %v", missing)
		res.failed = true
		return
	}
	if listOnly {
		res.status = fmt.Sprintf("%d/%d mapped tests exist (run deferred; -list-only)", total, total)
		return
	}

	// Execution (needs DB): each mapped test must terminate as pass; a skip fails.
	passed := 0
	var bad []string
	for _, pkg := range pkgs {
		actions, err := runGoTests(coreDir, pkg, byPkg[pkg])
		if err != nil {
			res.status = pkg + ": " + err.Error()
			res.failed = true
			return
		}
		for _, name := range byPkg[pkg] {
			switch actions[name] {
			case "pass":
				passed++
			case "skip":
				bad = append(bad, pkg+"::"+name+"(SKIPPED)")
			case "fail":
				bad = append(bad, pkg+"::"+name+"(FAILED)")
			default:
				bad = append(bad, pkg+"::"+name+"(NO RESULT)")
			}
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		res.status = fmt.Sprintf("%d/%d passed; problems: %v", passed, total, bad)
		res.failed = true
		return
	}
	res.status = fmt.Sprintf("%d/%d mapped tests passed", passed, total)
}

func evaluateOtherPlane(row *manifestRow, repoRoot string, res *rowResult) {
	var missing []string
	for _, ref := range row.PlaneTests {
		ok, err := planeTestPresent(repoRoot, ref)
		if err != nil {
			res.status = "plane test check error: " + err.Error()
			res.failed = true
			return
		}
		if !ok {
			missing = append(missing, ref.File+" :: "+ref.Marker)
		}
	}
	if len(missing) > 0 {
		res.status = fmt.Sprintf("MISSING plane test(s): %v", missing)
		res.failed = true
		return
	}
	res.status = fmt.Sprintf("covered in %s plane (owner %s): %d test file(s) present", row.Plane, row.Owner, len(row.PlaneTests))
}

func printReport(out io.Writer, results []rowResult, listOnly bool) {
	mode := "execute"
	if listOnly {
		mode = "list-only (no DB)"
	}
	_, _ = fmt.Fprintf(out, "\n=== S32 §16 edge-case gate — per-row report (mode: %s) ===\n", mode)
	for _, r := range results {
		tag := "PASS"
		if r.failed {
			tag = "FAIL"
		} else if r.class == classNonAutomatable {
			tag = "N/A "
		}
		_, _ = fmt.Fprintf(out, "[%s] %-42s %-15s %s\n", tag, r.edgeCase, r.class, r.status)
	}
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// findCoreDir returns the services/core directory. The gate is normally invoked
// from there (scenario 3 does `cd services/core`), but resolving it from the
// repo root keeps the manifest's "./internal/..." selectors valid regardless of
// the caller's working directory.
func findCoreDir(repoRoot, wd string) string {
	core := repoRoot + "/services/core"
	if _, err := os.Stat(core + "/go.mod"); err == nil {
		return core
	}
	return wd
}
