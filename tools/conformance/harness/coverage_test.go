package harness

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	schemaDir     = "../../../contract/protocol/schema"
	scenarioDir   = "../../../contract/protocol/scenarios"
	transcriptDir = "../../../contract/protocol/transcripts"
	oracleSrc     = "../oracle/src/server/im-go"
	negativeCases = "../../schema/negative-cases.json"
)

func loadSkip(t *testing.T) StepSet {
	t.Helper()
	skip, err := LoadStepSet(negativeCases)
	if err != nil {
		t.Fatal(err)
	}
	return skip
}

func loadGoldens(t *testing.T) ([]*Scenario, map[string]*Transcript) {
	t.Helper()
	scs, err := LoadScenarios(scenarioDir)
	if err != nil {
		t.Fatal(err)
	}
	trs := map[string]*Transcript{}
	for _, sc := range scs {
		tr, err := ReadTranscript(filepath.Join(transcriptDir, strings.ReplaceAll(sc.Name, "/", "__")+".json"))
		if err != nil {
			t.Fatal(err)
		}
		trs[sc.Name] = tr
	}
	return scs, trs
}

// Phase 0 acceptance: every frame name in the protocol has a golden case.
func TestEveryFrameNameHasAGoldenCase(t *testing.T) {
	v, err := LoadVocabulary(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	scs, trs := loadGoldens(t)
	var missing []string
	for _, u := range Coverage(v, scs, trs, loadSkip(t)) {
		if u.Count == 0 {
			missing = append(missing, u.Side+" "+u.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("no golden case for: %s", strings.Join(missing, ", "))
	}
	t.Logf("%d client actions, %d server frame names, all covered", len(v.Client), len(v.Server))
}

// Every name the golden set uses must be in the schemas.
func TestGoldensUseOnlyDefinedNames(t *testing.T) {
	v, err := LoadVocabulary(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	scs, trs := loadGoldens(t)
	if extra := Unlisted(v, scs, trs, loadSkip(t)); len(extra) > 0 {
		t.Errorf("used but not in the schemas: %s", strings.Join(extra, ", "))
	}
}

// Every Action*/Type* constant of the reference server must be in the
// schemas. The source copy is a build product of oracle/build-oracle.sh, so
// the test is skipped (visibly) when it has not been exported.
func TestSchemasCoverReferenceSourceConstants(t *testing.T) {
	files := []string{filepath.Join(oracleSrc, "protocol.go"), filepath.Join(oracleSrc, "hub.go")}
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			t.Skipf("reference source not exported (%s); run oracle/build-oracle.sh to enable this check", f)
		}
	}
	consts, err := SourceConstants(files...)
	if err != nil {
		t.Fatal(err)
	}
	v, err := LoadVocabulary(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, n := range append(append([]string{}, v.Client...), v.Server...) {
		known[n] = true
	}
	var missing []string
	actions := 0
	for name, value := range consts {
		if strings.HasPrefix(name, "Action") {
			actions++
		}
		if !known[value] {
			missing = append(missing, name+"="+value)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("source constants missing from the schemas: %s", strings.Join(missing, ", "))
	}
	// The plan's acceptance counts 69 Action constants at 33e23250.
	if actions != 69 {
		t.Errorf("found %d Action constants, the plan counts 69: recount the acceptance criterion", actions)
	}
	t.Logf("%d Action and %d Type constants, all in the schemas", actions, len(consts)-actions)
}

// The coverage check must notice a gap: drop the only scenario that sends
// "leave" and the only frames that carry it.
func TestCoverageNoticesAGap(t *testing.T) {
	v, err := LoadVocabulary(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	scs, trs := loadGoldens(t)
	var kept []*Scenario
	for _, sc := range scs {
		if sc.Name != "core/leave" {
			kept = append(kept, sc)
		}
	}
	for _, u := range Coverage(v, kept, trs, loadSkip(t)) {
		if u.Name == "leave" && u.Count != 0 {
			t.Errorf("%s leave still counted %d times without core/leave: %v", u.Side, u.Count, u.Where)
		}
	}
}

// Negative steps must not count: without the skip list, core/errors' unknown
// action shows up as undefined.
func TestNegativeStepsAreLeftOut(t *testing.T) {
	v, err := LoadVocabulary(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	scs, trs := loadGoldens(t)
	extra := Unlisted(v, scs, trs, StepSet{})
	if !strings.Contains(strings.Join(extra, ","), "client no_such_action") {
		t.Errorf("without the skip list the unknown action should be reported, got %v", extra)
	}
}
