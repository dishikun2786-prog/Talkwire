package redlines

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"example.com/imkit/tools/conformance/harness"
)

const (
	matrixPath  = "../../../contract/redlines/redlines.json"
	renderedMD  = "../../../contract/redlines/matrix.md"
	scenarioDir = "../../../contract/protocol/scenarios"
	serverDir   = "../../../server"
	androidDir  = "../../../android"
	webDir      = "../../../web"
)

func writtenTests(t *testing.T) Written {
	t.Helper()
	server, err := WrittenTests(serverDir)
	if err != nil {
		t.Fatal(err)
	}
	android, err := AndroidTests(androidDir)
	if err != nil {
		t.Fatal(err)
	}
	web, err := WebTests(webDir)
	if err != nil {
		t.Fatal(err)
	}
	return Written{"server": server, "android": android, "web": web}
}

// marked matches an "已写" mark in the summary or the details, not the legend.
var marked = regexp.MustCompile(`[0-9] 已写|，已写）`)

func load(t *testing.T) (*Matrix, []*harness.Scenario) {
	t.Helper()
	m, err := Load(matrixPath)
	if err != nil {
		t.Fatal(err)
	}
	scs, err := harness.LoadScenarios(scenarioDir)
	if err != nil {
		t.Fatal(err)
	}
	return m, scs
}

// clone deep-copies the matrix and scenarios so a test can break one thing.
func clone(t *testing.T, m *Matrix, scs []*harness.Scenario) (*Matrix, []*harness.Scenario) {
	t.Helper()
	var mc Matrix
	raw, _ := json.Marshal(m)
	if err := json.Unmarshal(raw, &mc); err != nil {
		t.Fatal(err)
	}
	out := make([]*harness.Scenario, len(scs))
	for i, sc := range scs {
		var c harness.Scenario
		raw, _ := json.Marshal(sc)
		if err := json.Unmarshal(raw, &c); err != nil {
			t.Fatal(err)
		}
		out[i] = &c
	}
	return &mc, out
}

func find(m *Matrix, id string) *Redline {
	for i := range m.Redlines {
		if m.Redlines[i].ID == id {
			return &m.Redlines[i]
		}
	}
	return nil
}

func scenario(scs []*harness.Scenario, name string) *harness.Scenario {
	for _, sc := range scs {
		if sc.Name == name {
			return sc
		}
	}
	return nil
}

func TestMatrixIsConsistent(t *testing.T) {
	m, scs := load(t)
	if problems := Check(m, scs); len(problems) > 0 {
		t.Fatalf("%d problem(s):\n%s", len(problems), strings.Join(problems, "\n"))
	}
	ids := map[string]bool{}
	for _, rl := range m.Redlines {
		ids[rl.ID] = true
	}
	// The plan fixes R1-R22 and C1-C10; the checker only requires no gaps.
	for _, id := range []string{"R1", "R22", "C1", "C10"} {
		if !ids[id] {
			t.Errorf("missing %s", id)
		}
	}
}

func TestRenderedFileIsCurrent(t *testing.T) {
	m, _ := load(t)
	current, err := os.ReadFile(renderedMD)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(current, Render(m, writtenTests(t))) {
		t.Fatal("matrix.md is out of date: run go run ./cmd/redlines write")
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	m, _ := load(t)
	written := writtenTests(t)
	first := Render(m, written)
	for i := 0; i < 5; i++ {
		if !bytes.Equal(first, Render(m, written)) {
			t.Fatal("Render output differs between runs")
		}
	}
}

// Every rule must actually fire: break one thing at a time and expect a
// problem that mentions it.
func TestEachRuleFires(t *testing.T) {
	base, baseScs := load(t)
	cases := []struct {
		name   string
		mutate func(m *Matrix, scs []*harness.Scenario)
		want   string
	}{
		{"cited step changed its action", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Golden[0].Steps[0] = "8:get"
		}, `step 8 is "pub" now, not "get"`},
		{"cited step does not exist", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Golden[0].Steps = append(find(m, "R1").Golden[0].Steps, "99:pub")
		}, "step 99 does not exist"},
		{"steps out of order", func(m *Matrix, _ []*harness.Scenario) {
			g := &find(m, "R1").Golden[0]
			g.Steps[0], g.Steps[1] = g.Steps[1], g.Steps[0]
		}, "increasing order"},
		{"malformed step reference", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Golden[0].Steps[0] = "eight"
		}, "is not N:verb"},
		{"unknown scenario", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Golden[0].Scenario = "core/nope"
		}, "no such scenario"},
		{"scenario not tagged", func(_ *Matrix, scs []*harness.Scenario) {
			scenario(scs, "core/group-messaging").Redlines = []string{"R2"}
		}, "scenario is not tagged R1"},
		{"tag without citation", func(_ *Matrix, scs []*harness.Scenario) {
			sc := scenario(scs, "core/notes")
			sc.Redlines = append(sc.Redlines, "R1")
		}, "scenario core/notes: tagged R1, but R1 cites no step of it"},
		{"tag of an unknown red line", func(_ *Matrix, scs []*harness.Scenario) {
			sc := scenario(scs, "core/notes")
			sc.Redlines = append(sc.Redlines, "R99")
		}, "tag R99 is not in the matrix"},
		{"malformed id", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R5").ID = "R6x"
		}, "id must be R<n> or C<n>"},
		{"numbering gap", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R5").ID = "R50"
		}, "expected R5 here"},
		{"client before server", func(m *Matrix, _ []*harness.Scenario) {
			m.Redlines[0], m.Redlines[len(m.Redlines)-1] = m.Redlines[len(m.Redlines)-1], m.Redlines[0]
		}, "must come before client red lines"},
		{"unknown task", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Planned[0].Task = "9.99"
		}, `unknown task "9.99"`},
		{"unused task", func(m *Matrix, _ []*harness.Scenario) {
			m.Tasks["9.9"] = "nothing uses this"
		}, "task 9.9 is listed but no planned case uses it"},
		{"test name without the id", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Planned[0].Name = "TestDuplicatePublish"
		}, "must carry the red line id"},
		{"R1_ is not satisfied by R11_", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Planned[0].Name = "TestR11_Something"
		}, "must carry the red line id (R1_...)"},
		{"platform of a planned case not on the red line", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Planned[0].Platform = "web"
		}, `platform "web" is not one of the red line's platforms`},
		{"a platform without any case", func(m *Matrix, _ []*harness.Scenario) {
			rl := find(m, "R7")
			var kept []Planned
			for _, p := range rl.Planned {
				if p.Platform != "ios" {
					kept = append(kept, p)
				}
			}
			rl.Planned = kept
		}, "R7: no case for platform ios"},
		{"violates without a strict case", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R18").Planned[0].Layer = "unit"
		}, "R18: the reference server violates it, so a strict case is required"},
		{"server red line with no golden and no reason", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R11").NoGolden = ""
		}, "must say why (noGolden)"},
		{"noGolden next to golden cases", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").NoGolden = "because"
		}, "noGolden must be empty"},
		{"golden on a host-only red line", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R14").Golden = []Golden{{Scenario: "core/notes", Steps: []string{"9:create_grp"}, Checks: "x"}}
		}, "golden cases only apply to red lines on the server"},
		{"client red line with a reference status", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "C1").Reference = "holds"
		}, "reference must be n/a exactly for client red lines"},
		{"unknown layer", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "R1").Planned[0].Layer = "vibes"
		}, `unknown layer "vibes"`},
		{"planned scenario does not exist", func(m *Matrix, _ []*harness.Scenario) {
			find(m, "C1").Planned[0].Scenario = "core/nope"
		}, `no scenario "core/nope"`},
		{"duplicate case", func(m *Matrix, _ []*harness.Scenario) {
			rl := find(m, "R10")
			rl.Planned = append(rl.Planned, rl.Planned[0])
		}, "case listed twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, scs := clone(t, base, baseScs)
			tc.mutate(m, scs)
			problems := Check(m, scs)
			joined := strings.Join(problems, "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tc.want, joined)
			}
		})
	}
}

// The server's red-line tests are found, marked written, and one that the
// matrix does not list is reported.
func TestWrittenTests(t *testing.T) {
	dir := t.TempDir()
	src := "package main\n\nimport \"testing\"\n\n" +
		"func TestR1_ConcurrentDuplicatePublish(t *testing.T) {}\n" +
		"func TestR2_NotInTheMatrix(t *testing.T) {}\n" +
		"func TestSomethingElse(t *testing.T) {}\n" +
		"func helper(t *testing.T) {}\n"
	if err := os.WriteFile(dir+"/a_test.go", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/b.go", []byte("package main\n\nfunc TestR3_NotATestFile(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	written, err := WrittenTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 3 || !written["TestR1_ConcurrentDuplicatePublish"] || !written["TestSomethingElse"] || written["TestR3_NotATestFile"] {
		t.Fatalf("written: %v", written)
	}
	m, _ := load(t)
	problems := CheckWritten(m, Written{"server": written})
	if len(problems) != 1 || !strings.Contains(problems[0], "TestR2_NotInTheMatrix") {
		t.Fatalf("problems: %v", problems)
	}
	out := string(Render(m, Written{"server": written}))
	if !strings.Contains(out, "`TestR1_ConcurrentDuplicatePublish`：服务端 1.11 已写") {
		t.Fatal("a written server case is not marked in the summary")
	}
	if nothing := string(Render(m, nil)); marked.MatchString(nothing) || !strings.Contains(nothing, "服务端 0/") {
		t.Fatal("nothing is written, yet a case is marked")
	}
}

// Android red-line tests are found in src/test only, marked written per
// platform, and one that the matrix does not list is reported.
func TestAndroidTests(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("mod/src/test/kotlin/a/ATest.kt", "class ATest {\n"+
		"    @Test\n    fun C1_IgnoresDuplicatedFrames() {}\n"+
		"    @Test\n    fun C1_NotInTheMatrix() = runBlocking {}\n"+
		"    @Test\n    fun c1_lowerCaseIsNotARedLineTest() {}\n"+
		"    private fun helper() {}\n}\n")
	write("mod/src/main/kotlin/a/Main.kt", "fun C2_NotATest() {}\n")
	write("mod/src/testFixtures/kotlin/a/Fixture.kt", "fun C3_NotATestEither() {}\n")
	write("mod/build/tmp/src/test/kotlin/Gen.kt", "fun C4_Generated() {}\n")
	android, err := AndroidTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(android) != 2 || !android["C1_IgnoresDuplicatedFrames"] || !android["C1_NotInTheMatrix"] {
		t.Fatalf("android: %v", android)
	}
	m, _ := load(t)
	written := Written{"android": android}
	problems := CheckWritten(m, written)
	if len(problems) != 1 || !strings.Contains(problems[0], "android test C1_NotInTheMatrix") {
		t.Fatalf("problems: %v", problems)
	}
	out := string(Render(m, written))
	if !strings.Contains(out, "`C1_IgnoresDuplicatedFrames`：Android 3.10 已写") {
		t.Fatal("a written Android case is not marked in the summary")
	}
	if strings.Contains(out, "`C1_ReconnectAfterInjectedDisconnect`：Android 3.10 已写") {
		t.Fatal("an Android case is marked without its test")
	}
	if strings.Contains(out, "Web 5.8 已写") {
		t.Fatal("the same name on another platform is marked")
	}
}

// Web red-line cases are found in test files only (Vitest *.test.ts(x),
// Playwright *.spec.ts) outside node_modules and build output, marked written
// per platform, and one that the matrix does not list is reported.
func TestWebTests(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, src string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("packages/client/test/a.test.ts", "describe(\"x\", () => {\n"+
		"  it(\"C1_IgnoresDuplicatedFrames\", async () => {});\n"+
		"  it('C1_NotInTheMatrix', () => {});\n"+
		"  it(\"c1_lowerCaseIsNotARedLineTest\", () => {});\n"+
		"  it(\"plain name\", () => {});\n});\n")
	write("packages/react/test/b.test.tsx", "it(`C7_BubblesFromContentType`, () => {});\n")
	write("e2e/c.spec.ts", "test(\n  \"R7_ClearOfflineReplay\", async () => {});\n")
	write("packages/client/src/main.ts", "it(\"C2_NotATestFile\", () => {});\n")
	write("node_modules/dep/x.test.ts", "it(\"C3_Dependency\", () => {});\n")
	write("packages/client/dist/y.test.ts", "it(\"C4_BuildOutput\", () => {});\n")
	web, err := WebTests(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(web) != 4 || !web["C1_IgnoresDuplicatedFrames"] || !web["C1_NotInTheMatrix"] || !web["C7_BubblesFromContentType"] || !web["R7_ClearOfflineReplay"] {
		t.Fatalf("web: %v", web)
	}
	m, _ := load(t)
	written := Written{"web": web}
	problems := CheckWritten(m, written)
	if len(problems) != 1 || !strings.Contains(problems[0], "web test C1_NotInTheMatrix") {
		t.Fatalf("problems: %v", problems)
	}
	out := string(Render(m, written))
	if !strings.Contains(out, "Web 5.8 已写") || !strings.Contains(out, "Web 5.9 已写") {
		t.Fatal("a written web case is not marked in the summary")
	}
	if strings.Contains(out, "Android 3.10 已写") {
		t.Fatal("the same name on another platform is marked")
	}
}
