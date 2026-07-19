package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const prdFixture = `
## 15. Something else

text.

## 16. Edge-case contract

| Edge case | Required behavior |
|---|---|
| Missing or stale COGS | Block; name component |
| Duplicate event | Update open event inside dedup window |
| No events | Explicit no-action state |

---

## 17. Non-functional requirements
`

func TestParsePRDSection16_ExtractsFirstColumnInOrder(t *testing.T) {
	got, err := parsePRDSection16([]byte(prdFixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := []string{"Missing or stale COGS", "Duplicate event", "No events"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParsePRDSection16_MissingTableIsError(t *testing.T) {
	if _, err := parsePRDSection16([]byte("## 1. Intro\n\nno table here\n")); err == nil {
		t.Fatal("expected an error when §16 table is absent")
	}
}

// canonical set used across validation tests.
func canonical3() []string {
	return []string{"Missing or stale COGS", "Duplicate event", "No events"}
}

func goodManifest() *manifest {
	return &manifest{Rows: []manifestRow{
		{ID: "a", EdgeCase: "Missing or stale COGS", Classification: classGoGate,
			Tests: []testRef{{Package: "./internal/cost", Name: "TestX"}}},
		{ID: "b", EdgeCase: "Duplicate event", Classification: classOtherScenario, Scenario: "adversarial replay"},
		{ID: "c", EdgeCase: "No events", Classification: classNonAutomatable, Owner: "web_frontend", Reason: "needs fixture"},
	}}
}

func TestValidateManifest_HappyPathNoProblems(t *testing.T) {
	if p := validateManifest(goodManifest(), canonical3()); len(p) != 0 {
		t.Fatalf("expected no problems, got %v", p)
	}
}

func TestValidateManifest_UnclassifiedCanonicalRowFails(t *testing.T) {
	m := goodManifest()
	m.Rows = m.Rows[:2] // drop "No events"
	p := validateManifest(m, canonical3())
	if !containsSubstr(p, "unclassified canonical") || !containsSubstr(p, "No events") {
		t.Fatalf("expected unclassified-canonical problem for 'No events', got %v", p)
	}
}

func TestValidateManifest_StaleRowFails(t *testing.T) {
	m := goodManifest()
	m.Rows = append(m.Rows, manifestRow{ID: "z", EdgeCase: "Not A Real Row", Classification: classNonAutomatable, Owner: "x", Reason: "y"})
	if !containsSubstr(validateManifest(m, canonical3()), "stale manifest row") {
		t.Fatal("expected a stale-row problem")
	}
}

func TestValidateManifest_DuplicateIDFails(t *testing.T) {
	m := goodManifest()
	m.Rows[1].ID = "a"
	if !containsSubstr(validateManifest(m, canonical3()), "duplicate row id") {
		t.Fatal("expected a duplicate-id problem")
	}
}

func TestValidateManifest_GoGateNeedsTests(t *testing.T) {
	m := goodManifest()
	m.Rows[0].Tests = nil
	if !containsSubstr(validateManifest(m, canonical3()), "go_gate needs at least one test") {
		t.Fatal("expected go_gate-needs-tests problem")
	}
}

func TestValidateManifest_NonAutomatableNeedsOwnerAndReason(t *testing.T) {
	m := goodManifest()
	m.Rows[2].Owner = ""
	if !containsSubstr(validateManifest(m, canonical3()), "non_automatable needs an owner") {
		t.Fatal("expected owner-required problem")
	}
	m = goodManifest()
	m.Rows[2].Reason = ""
	if !containsSubstr(validateManifest(m, canonical3()), "non_automatable needs a reason") {
		t.Fatal("expected reason-required problem")
	}
}

func TestValidateManifest_OtherScenarioNeedsScenario(t *testing.T) {
	m := goodManifest()
	m.Rows[1].Scenario = ""
	if !containsSubstr(validateManifest(m, canonical3()), "other_scenario needs a scenario") {
		t.Fatal("expected scenario-required problem")
	}
}

func TestValidateManifest_UnknownClassificationFails(t *testing.T) {
	m := goodManifest()
	m.Rows[0].Classification = "bogus"
	if !containsSubstr(validateManifest(m, canonical3()), "unknown classification") {
		t.Fatal("expected unknown-classification problem")
	}
}

func TestValidateManifest_OtherPlaneNeedsOwnerAndPlaneTests(t *testing.T) {
	m := &manifest{Rows: []manifestRow{
		{ID: "a", EdgeCase: "Missing or stale COGS", Classification: classOtherPlane},
	}}
	p := validateManifest(m, []string{"Missing or stale COGS"})
	if !containsSubstr(p, "other_plane needs an owner") || !containsSubstr(p, "other_plane needs at least one plane_test") {
		t.Fatalf("expected owner+plane_test problems, got %v", p)
	}
}

func TestParseGoTestJSON_ClassifiesTerminalActions(t *testing.T) {
	stream := strings.Join([]string{
		`{"Action":"run","Test":"TestA"}`,
		`{"Action":"pass","Test":"TestA"}`,
		`{"Action":"run","Test":"TestB"}`,
		`{"Action":"skip","Test":"TestB"}`,
		`{"Action":"run","Test":"TestC"}`,
		`{"Action":"fail","Test":"TestC"}`,
		`{"Action":"pass"}`, // package-level, ignored (no Test)
	}, "\n")
	got, err := parseGoTestJSON([]byte(stream))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for name, want := range map[string]string{"TestA": "pass", "TestB": "skip", "TestC": "fail"} {
		if got[name] != want {
			t.Fatalf("%s: got %q want %q", name, got[name], want)
		}
	}
}

func TestParseGoTestJSON_CompileFailureIsError(t *testing.T) {
	if _, err := parseGoTestJSON([]byte("# services/core/internal/foo\nfoo.go:1: undefined: bar\n")); err == nil {
		t.Fatal("expected a compile-failure error when no test results are present")
	}
}

// --- real-toolchain existence checks (no DB; compile-only `go test -list`) ---

func coreDirForTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRepoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "services", "core")
}

func TestListExistingTests_RealTestFoundBogusMissing(t *testing.T) {
	core := coreDirForTest(t)
	missing, err := listExistingTests(core, "./internal/httpapi",
		[]string{"TestEdgeCase_NoEvents_ExplicitNoActionState", "TestThisNameDoesNotExist_164"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(missing) != 1 || missing[0] != "TestThisNameDoesNotExist_164" {
		t.Fatalf("expected only the bogus test missing, got %v", missing)
	}
}

// TestRealManifestIsCompleteAndExecutable is the standing regression guard the
// issue asks for: it fails in the ordinary (no-DB) Go CI job if the manifest
// drifts from PRD §16, if any go_gate test is renamed/removed (zero -list
// matches), or if an other_plane test file/marker disappears.
func TestRealManifestIsCompleteAndExecutable(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := findRepoRoot(wd)
	if err != nil {
		t.Fatal(err)
	}
	core := filepath.Join(root, "services", "core")

	prd, err := os.ReadFile(filepath.Join(root, "docs", "PRD.md"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := parsePRDSection16(prd)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loadManifest(filepath.Join(root, "tools", "integration", "section16_manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if problems := validateManifest(m, canonical); len(problems) > 0 {
		for _, p := range problems {
			t.Errorf("manifest problem: %v", p)
		}
		t.FailNow()
	}
	for _, row := range m.Rows {
		switch row.Classification {
		case classGoGate:
			for pkg, names := range groupTestsByPackage(row.Tests) {
				missing, err := listExistingTests(core, pkg, names)
				if err != nil {
					t.Fatalf("row %q: %v", row.EdgeCase, err)
				}
				if len(missing) > 0 {
					t.Errorf("row %q: mapped tests not found in %s: %v", row.EdgeCase, pkg, missing)
				}
			}
		case classOtherPlane:
			for _, ref := range row.PlaneTests {
				ok, err := planeTestPresent(root, ref)
				if err != nil {
					t.Fatalf("row %q: %v", row.EdgeCase, err)
				}
				if !ok {
					t.Errorf("row %q: plane test missing: %s :: %s", row.EdgeCase, ref.File, ref.Marker)
				}
			}
		}
	}
}

func containsSubstr(errs []error, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), sub) {
			return true
		}
	}
	return false
}
