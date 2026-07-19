// Command section16gate is the S32 integration gate for the PRD §16 edge-case
// contract (issue #164). It reads tools/integration/section16_manifest.json,
// cross-checks it against the canonical §16 table in docs/PRD.md, and — for
// every offline-testable row — executes the mapped Go tests explicitly, failing
// if any canonical row is unclassified, any mapped test was renamed/removed
// (zero `go test -list` matches), or any mapped test is skipped rather than run.
//
// This replaces the previous scenario-3 command `go test -run 'TestEdgeCase'`,
// which selected only the three TestEdgeCase* functions and silently ignored
// every other required §16 row and its owning-step sibling tests.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Classification values a manifest row may carry.
const (
	classGoGate         = "go_gate"
	classOtherScenario  = "other_scenario"
	classOtherPlane     = "other_plane"
	classNonAutomatable = "non_automatable"
)

// testRef names one Go test the gate runs (package is a `go test` pattern
// relative to the core module root, e.g. "./internal/cost").
type testRef struct {
	Package string `json:"package"`
	Name    string `json:"name"`
}

// planeRef names one non-Go test whose existence the gate verifies (file
// relative to the repo root, marker is a substring that must be present).
type planeRef struct {
	File   string `json:"file"`
	Marker string `json:"marker"`
}

type manifestRow struct {
	ID             string     `json:"id"`
	EdgeCase       string     `json:"edge_case"`
	Classification string     `json:"classification"`
	Tests          []testRef  `json:"tests"`
	Scenario       string     `json:"scenario"`
	Plane          string     `json:"plane"`
	PlaneTests     []planeRef `json:"plane_tests"`
	Owner          string     `json:"owner"`
	Reason         string     `json:"reason"`
	Note           string     `json:"note"`
}

type manifest struct {
	PRDSection string        `json:"prd_section"`
	Rows       []manifestRow `json:"rows"`
}

func loadManifest(path string) (*manifest, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // gate-owned path, not user input.
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", path, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return &m, nil
}

// parsePRDSection16 extracts the canonical edge-case names (first column) from
// the "## 16. Edge-case contract" markdown table in docs/PRD.md, in order. The
// gate treats this as the authoritative row set: any name here without a
// matching manifest row is an unclassified canonical row and fails the gate.
func parsePRDSection16(prd []byte) ([]string, error) {
	sc := bufio.NewScanner(bytes.NewReader(prd))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var names []string
	inSection, inTable := false, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "## ") {
			// A new top-level section ends the §16 table.
			if inSection {
				break
			}
			inSection = strings.HasPrefix(line, "## 16.")
			continue
		}
		if !inSection {
			continue
		}
		if !strings.HasPrefix(line, "|") {
			if inTable {
				break // blank/non-table line terminates the table.
			}
			continue
		}
		cells := splitTableRow(line)
		if len(cells) == 0 {
			continue
		}
		first := cells[0]
		// Skip the header row and the |---|---| separator.
		if strings.EqualFold(first, "Edge case") {
			inTable = true
			continue
		}
		if isSeparatorCell(first) {
			continue
		}
		inTable = true
		if first != "" {
			names = append(names, first)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan PRD: %w", err)
	}
	if len(names) == 0 {
		return nil, errors.New("PRD §16 table not found or empty")
	}
	return names, nil
}

func splitTableRow(line string) []string {
	trimmed := strings.Trim(line, "|")
	parts := strings.Split(trimmed, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func isSeparatorCell(cell string) bool {
	if cell == "" {
		return false
	}
	for _, r := range cell {
		if r != '-' && r != ':' {
			return false
		}
	}
	return true
}

// validateManifest returns every structural problem: an unclassified canonical
// row, a stale manifest entry, a duplicate id, an unknown classification, or a
// classification missing its required fields. An empty slice means the manifest
// is complete and well-formed against the canonical row set.
func validateManifest(m *manifest, canonical []string) []error {
	var problems []error

	byEdgeCase := make(map[string]*manifestRow, len(m.Rows))
	seenID := make(map[string]bool, len(m.Rows))
	for i := range m.Rows {
		row := &m.Rows[i]
		if row.ID == "" {
			problems = append(problems, fmt.Errorf("row %q: empty id", row.EdgeCase))
		} else if seenID[row.ID] {
			problems = append(problems, fmt.Errorf("duplicate row id %q", row.ID))
		}
		seenID[row.ID] = true

		if _, dup := byEdgeCase[row.EdgeCase]; dup {
			problems = append(problems, fmt.Errorf("duplicate edge_case %q", row.EdgeCase))
		}
		byEdgeCase[row.EdgeCase] = row

		problems = append(problems, validateRowFields(row)...)
	}

	canonicalSet := make(map[string]bool, len(canonical))
	for _, name := range canonical {
		canonicalSet[name] = true
		if _, ok := byEdgeCase[name]; !ok {
			problems = append(problems, fmt.Errorf("unclassified canonical §16 row: %q has no manifest entry", name))
		}
	}
	for _, row := range m.Rows {
		if !canonicalSet[row.EdgeCase] {
			problems = append(problems, fmt.Errorf("stale manifest row %q: not a PRD §16 edge case", row.EdgeCase))
		}
	}
	return problems
}

func validateRowFields(row *manifestRow) []error {
	var problems []error
	label := row.EdgeCase
	switch row.Classification {
	case classGoGate:
		if len(row.Tests) == 0 {
			problems = append(problems, fmt.Errorf("row %q: go_gate needs at least one test", label))
		}
		for _, t := range row.Tests {
			if t.Package == "" || t.Name == "" {
				problems = append(problems, fmt.Errorf("row %q: test needs package and name (got %q/%q)", label, t.Package, t.Name))
			}
		}
	case classOtherScenario:
		if strings.TrimSpace(row.Scenario) == "" {
			problems = append(problems, fmt.Errorf("row %q: other_scenario needs a scenario name", label))
		}
	case classOtherPlane:
		if row.Owner == "" {
			problems = append(problems, fmt.Errorf("row %q: other_plane needs an owner", label))
		}
		if len(row.PlaneTests) == 0 {
			problems = append(problems, fmt.Errorf("row %q: other_plane needs at least one plane_test", label))
		}
		for _, p := range row.PlaneTests {
			if p.File == "" || p.Marker == "" {
				problems = append(problems, fmt.Errorf("row %q: plane_test needs file and marker (got %q/%q)", label, p.File, p.Marker))
			}
		}
	case classNonAutomatable:
		if row.Owner == "" {
			problems = append(problems, fmt.Errorf("row %q: non_automatable needs an owner", label))
		}
		if strings.TrimSpace(row.Reason) == "" {
			problems = append(problems, fmt.Errorf("row %q: non_automatable needs a reason", label))
		}
	case "":
		problems = append(problems, fmt.Errorf("row %q: missing classification", label))
	default:
		problems = append(problems, fmt.Errorf("row %q: unknown classification %q", label, row.Classification))
	}
	return problems
}

// listExistingTests asks the Go toolchain which of the requested test names
// actually exist in a package (`go test -list`, which compiles but never runs
// the tests, so it needs no database). It returns the names that are MISSING —
// a non-empty result means a mapped test was renamed, removed, or its selector
// matches zero tests, all of which must fail the gate.
func listExistingTests(coreDir, pkg string, names []string) (missing []string, err error) {
	if len(names) == 0 {
		return nil, nil
	}
	pattern := "^(" + strings.Join(escapeAll(names), "|") + ")$"
	cmd := exec.Command("go", "test", "-list", pattern, pkg)
	cmd.Dir = coreDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return nil, fmt.Errorf("go test -list %s %s: %w\n%s", pattern, pkg, runErr, out)
	}
	found := make(map[string]bool)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		found[strings.TrimSpace(sc.Text())] = true
	}
	for _, n := range names {
		if !found[n] {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	return missing, nil
}

// runGoTests runs the named tests and returns each test's terminal action
// ("pass"/"fail"/"skip") parsed from `go test -json`. It needs DATABASE_URL and
// a live Postgres, so it runs only inside the compose integration stack.
func runGoTests(coreDir, pkg string, names []string) (map[string]string, error) {
	pattern := "^(" + strings.Join(escapeAll(names), "|") + ")$"
	cmd := exec.Command("go", "test", "-json", "-run", pattern, pkg)
	cmd.Dir = coreDir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, _ := cmd.CombinedOutput() // a test failure is a non-zero exit; parse it.
	return parseGoTestJSON(out)
}

// parseGoTestJSON reduces a `go test -json` event stream to each test's terminal
// action. Non-JSON lines (e.g. a build error printed to stderr) are surfaced as
// an error so a package that does not even compile fails the gate loudly.
func parseGoTestJSON(out []byte) (map[string]string, error) {
	result := make(map[string]string)
	var buildErr []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if trimmed[0] != '{' {
			buildErr = append(buildErr, string(trimmed))
			continue
		}
		var ev struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := json.Unmarshal(trimmed, &ev); err != nil {
			buildErr = append(buildErr, string(trimmed))
			continue
		}
		if ev.Test == "" {
			continue
		}
		switch ev.Action {
		case "pass", "fail", "skip":
			result[ev.Test] = ev.Action
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan go test -json: %w", err)
	}
	if len(result) == 0 && len(buildErr) > 0 {
		return nil, fmt.Errorf("no test results (compile/run failure):\n%s", strings.Join(buildErr, "\n"))
	}
	return result, nil
}

func scenarioPresent(runAll []byte, scenario string) bool {
	return bytes.Contains(runAll, []byte(scenario))
}

func planeTestPresent(repoRoot string, ref planeRef) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, ref.File)) //nolint:gosec // gate-owned manifest path.
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return bytes.Contains(raw, []byte(ref.Marker)), nil
}

// escapeAll quotes each name for safe embedding in a `-run`/`-list` alternation.
// Test names are Go identifiers, so this is a defensive belt only.
func escapeAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.NewReplacer(
			".", `\.`, "(", `\(`, ")", `\)`, "|", `\|`,
			"+", `\+`, "*", `\*`, "?", `\?`, "$", `\$`, "^", `\^`,
		).Replace(n)
	}
	return out
}

// findRepoRoot walks up from start until it finds the marker file docs/PRD.md.
func findRepoRoot(start string) (string, error) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "docs", "PRD.md")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("repo root not found (no docs/PRD.md above " + start + ")")
		}
		dir = parent
	}
}

// groupTestsByPackage collapses a row's tests into one selector per package.
func groupTestsByPackage(tests []testRef) map[string][]string {
	byPkg := make(map[string][]string)
	for _, t := range tests {
		byPkg[t.Package] = append(byPkg[t.Package], t.Name)
	}
	return byPkg
}
