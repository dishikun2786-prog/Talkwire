// Package redlines checks the red-line matrix (contract/redlines/redlines.json)
// against the scenarios it cites, and renders it as Markdown.
//
// A red line is a rule the framework must not break. The matrix ties each one
// to recorded golden-transcript steps (what the reference server does today)
// and to planned automated tests (task in the plan, platform, test name). The
// checks keep the two from drifting apart: cited steps must still exist and
// still be the same action, scenario tags and matrix entries must agree, and
// every platform a red line applies to must have at least one case.
package redlines

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"example.com/imkit/tools/conformance/harness"
)

// Matrix is the whole registry file.
type Matrix struct {
	About      string            `json:"about"`
	Platforms  map[string]string `json:"platforms"`
	Layers     map[string]string `json:"layers"`
	References map[string]string `json:"references"`
	Tasks      map[string]string `json:"tasks"`
	Redlines   []Redline         `json:"redlines"`
}

// Redline is one rule and the cases that cover it.
type Redline struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Platforms []string  `json:"platforms"`
	Reference string    `json:"reference"`
	Note      string    `json:"note,omitempty"`
	NoGolden  string    `json:"noGolden,omitempty"`
	Golden    []Golden  `json:"golden"`
	Planned   []Planned `json:"planned"`
}

// Golden cites recorded steps of one scenario. Each step is "N:verb", where
// verb is the request's action for a send step and the step kind otherwise.
type Golden struct {
	Scenario string   `json:"scenario"`
	Steps    []string `json:"steps"`
	Checks   string   `json:"checks"`
}

// Planned is an automated test: one still to be written, or, for the server
// and Android, one found among their tests (see Written).
type Planned struct {
	Task     string `json:"task"`
	Layer    string `json:"layer"`
	Platform string `json:"platform"`
	Name     string `json:"name"`
	Scenario string `json:"scenario,omitempty"`
	Checks   string `json:"checks"`
}

// Load reads the registry, rejecting unknown fields.
func Load(path string) (*Matrix, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Matrix
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

var idPattern = regexp.MustCompile(`^([RC])([1-9][0-9]*)$`)

func isClient(id string) bool { return strings.HasPrefix(id, "C") }

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Check returns every problem found; an empty result means the matrix is
// consistent with the scenarios.
func Check(m *Matrix, scenarios []*harness.Scenario) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	byName := map[string]*harness.Scenario{}
	for _, sc := range scenarios {
		byName[sc.Name] = sc
	}

	// Ids: R1, R2, ... then C1, C2, ..., each run starting at 1 with no gaps.
	next := map[string]int{"R": 1, "C": 1}
	seenClient := false
	byID := map[string]*Redline{}
	for i := range m.Redlines {
		rl := &m.Redlines[i]
		match := idPattern.FindStringSubmatch(rl.ID)
		if match == nil {
			add("%q: id must be R<n> or C<n>", rl.ID)
			continue
		}
		n, _ := strconv.Atoi(match[2])
		if match[1] == "C" {
			seenClient = true
		} else if seenClient {
			add("%s: server red lines (R) must come before client red lines (C)", rl.ID)
		}
		if n != next[match[1]] {
			add("%s: expected %s%d here (ids are numbered without gaps)", rl.ID, match[1], next[match[1]])
		}
		next[match[1]] = n + 1
		if byID[rl.ID] != nil {
			add("%s: duplicate id", rl.ID)
		}
		byID[rl.ID] = rl
	}

	usedTasks := map[string]bool{}
	for i := range m.Redlines {
		rl := &m.Redlines[i]
		checkRedline(m, rl, byName, usedTasks, add)
	}

	// Scenario tags and golden citations must agree in both directions.
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for _, tag := range byName[name].Redlines {
			rl := byID[tag]
			if rl == nil {
				add("scenario %s: tag %s is not in the matrix", name, tag)
				continue
			}
			cited := false
			for _, g := range rl.Golden {
				if g.Scenario == name {
					cited = true
				}
			}
			if !cited {
				add("scenario %s: tagged %s, but %s cites no step of it", name, tag, tag)
			}
		}
	}

	tasks := make([]string, 0, len(m.Tasks))
	for t := range m.Tasks {
		tasks = append(tasks, t)
	}
	sort.Strings(tasks)
	for _, t := range tasks {
		if !usedTasks[t] {
			add("task %s is listed but no planned case uses it", t)
		}
	}
	return problems
}

func checkRedline(m *Matrix, rl *Redline, byName map[string]*harness.Scenario, usedTasks map[string]bool, add func(string, ...any)) {
	id := rl.ID
	if strings.TrimSpace(rl.Title) == "" {
		add("%s: title is empty", id)
	}
	if len(rl.Platforms) == 0 {
		add("%s: no platforms", id)
	}
	seenPlatform := map[string]bool{}
	for _, p := range rl.Platforms {
		if _, ok := m.Platforms[p]; !ok {
			add("%s: unknown platform %q", id, p)
		}
		if seenPlatform[p] {
			add("%s: platform %q listed twice", id, p)
		}
		seenPlatform[p] = true
	}
	if _, ok := m.References[rl.Reference]; !ok {
		add("%s: unknown reference status %q", id, rl.Reference)
	}
	if isClient(id) != (rl.Reference == "n/a") {
		add("%s: reference must be n/a exactly for client red lines", id)
	}

	server := contains(rl.Platforms, "server")
	switch {
	case len(rl.Golden) > 0 && !server:
		add("%s: golden cases only apply to red lines on the server", id)
	case len(rl.Golden) > 0 && rl.NoGolden != "":
		add("%s: has golden cases, so noGolden must be empty", id)
	case len(rl.Golden) == 0 && server && strings.TrimSpace(rl.NoGolden) == "":
		add("%s: a server red line with no golden case must say why (noGolden)", id)
	case !server && rl.NoGolden != "":
		add("%s: noGolden only applies to red lines on the server", id)
	}

	covered := map[string]bool{}
	for _, g := range rl.Golden {
		covered["server"] = true
		checkGolden(id, g, byName, add)
	}
	seenCase := map[string]bool{}
	strict := false
	for _, p := range rl.Planned {
		where := fmt.Sprintf("%s: planned %s", id, p.Name)
		if _, ok := m.Tasks[p.Task]; !ok {
			add("%s: unknown task %q", where, p.Task)
		}
		usedTasks[p.Task] = true
		if _, ok := m.Layers[p.Layer]; !ok {
			add("%s: unknown layer %q", where, p.Layer)
		}
		if p.Layer == "strict" {
			strict = true
		}
		if !contains(rl.Platforms, p.Platform) {
			add("%s: platform %q is not one of the red line's platforms", where, p.Platform)
		}
		covered[p.Platform] = true
		if !strings.Contains(p.Name, id+"_") {
			add("%s: test name must carry the red line id (%s_...)", where, id)
		}
		if strings.TrimSpace(p.Checks) == "" {
			add("%s: checks is empty", where)
		}
		if p.Scenario != "" && byName[p.Scenario] == nil {
			add("%s: no scenario %q", where, p.Scenario)
		}
		key := p.Platform + "|" + p.Name
		if seenCase[key] {
			add("%s: %s case listed twice", where, p.Platform)
		}
		seenCase[key] = true
	}
	for _, p := range rl.Platforms {
		if !covered[p] {
			add("%s: no case for platform %s", id, p)
		}
	}
	if rl.Reference == "violates" && !strict {
		add("%s: the reference server violates it, so a strict case is required", id)
	}
}

// Written lists, per platform, the tests found in the code: which planned
// cases exist. The server, the Node host SDK, Android and the web are
// scanned; iOS not yet.
type Written map[string]map[string]bool

// Has reports whether the platform's test of that name exists.
func (w Written) Has(platform, name string) bool { return w[platform][name] }

// ScannedPlatforms are the platforms whose tests Written can hold, in display order.
var ScannedPlatforms = []string{"server", "host", "android", "web"}

var testFunc = regexp.MustCompile(`(?m)^func (Test\w+)\(\w+ \*testing\.T\)`)

// WrittenTests returns the names of the Go test functions in the *_test.go
// files under dir, recursively: which server cases of the matrix exist.
func WrittenTests(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != dir && (strings.HasPrefix(name, ".") || name == "testdata" || name == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range testFunc.FindAllStringSubmatch(string(src), -1) {
			out[m[1]] = true
		}
		return nil
	})
	return out, err
}

var kotlinRedlineTest = regexp.MustCompile(`(?m)^\s*fun ([RC][1-9][0-9]*_\w+)\(\)`)

// AndroidTests returns the Kotlin test functions named after a red line
// (R<n>_... or C<n>_...) in the unit-test sources (src/test) of the modules
// under dir: which Android cases of the matrix exist.
func AndroidTests(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); path != dir && (strings.HasPrefix(name, ".") || name == "build") {
				return filepath.SkipDir
			}
			return nil
		}
		slashed := filepath.ToSlash(path)
		if !strings.HasSuffix(slashed, ".kt") || !strings.Contains(slashed, "/src/test/") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range kotlinRedlineTest.FindAllStringSubmatch(string(src), -1) {
			out[m[1]] = true
		}
		return nil
	})
	return out, err
}

// The name is the first argument, quoted with ", ' or `.
var webRedlineTest = regexp.MustCompile(`(?m)\b(?:it|test)\(\s*["'` + "`" + `]([RC][1-9][0-9]*_\w+)["'` + "`" + `]`)

// WebTests returns the Vitest and Playwright cases named after a red line
// (it("R<n>_...") or test("C<n>_...")) in the test files (*.test.ts(x),
// *.spec.ts) under dir, outside node_modules and build output: which web
// cases of the matrix exist.
func WebTests(dir string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch name := d.Name(); {
			case path == dir:
			case strings.HasPrefix(name, "."), name == "node_modules", name == "dist", name == "test-results", name == "playwright-report":
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".test.ts") && !strings.HasSuffix(name, ".test.tsx") && !strings.HasSuffix(name, ".spec.ts") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range webRedlineTest.FindAllStringSubmatch(string(src), -1) {
			out[m[1]] = true
		}
		return nil
	})
	return out, err
}

// HostTests returns the node:test cases of the Node host SDK named after a
// red line (test("R<n>_...")) in its test files: which host cases of the
// matrix exist. The files and the naming are the web's, so is the scan.
func HostTests(dir string) (map[string]bool, error) { return WebTests(dir) }

// redlineTestPatterns tell, per scanned platform, which test names are named
// after a red line; the group is the red line's id.
var redlineTestPatterns = map[string]*regexp.Regexp{
	"server":  regexp.MustCompile(`^Test(R[1-9][0-9]*)_`),
	"android": regexp.MustCompile(`^([RC][1-9][0-9]*)_`),
	"web":     regexp.MustCompile(`^([RC][1-9][0-9]*)_`),
	"host":    regexp.MustCompile(`^(R[1-9][0-9]*)_`),
}

// CheckWritten reports tests named after a red line (server TestR<n>_...,
// host R<n>_..., Android and web R<n>_... or C<n>_...) that the matrix does not list as a
// case of that platform: a red-line test written without its row in the matrix.
func CheckWritten(m *Matrix, written Written) []string {
	var problems []string
	for _, platform := range ScannedPlatforms {
		listed := map[string]bool{}
		for _, rl := range m.Redlines {
			for _, p := range rl.Planned {
				if p.Platform == platform {
					listed[p.Name] = true
				}
			}
		}
		names := make([]string, 0, len(written[platform]))
		for name := range written[platform] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			match := redlineTestPatterns[platform].FindStringSubmatch(name)
			if match == nil || listed[name] {
				continue
			}
			problems = append(problems, fmt.Sprintf("%s test %s is not in the matrix: list it as a %s case of %s", platform, name, platform, match[1]))
		}
	}
	return problems
}

func checkGolden(id string, g Golden, byName map[string]*harness.Scenario, add func(string, ...any)) {
	where := fmt.Sprintf("%s: golden %s", id, g.Scenario)
	sc := byName[g.Scenario]
	if sc == nil {
		add("%s: no such scenario", where)
		return
	}
	if !contains(sc.Redlines, id) {
		add("%s: scenario is not tagged %s", where, id)
	}
	if len(g.Steps) == 0 {
		add("%s: no steps", where)
	}
	if strings.TrimSpace(g.Checks) == "" {
		add("%s: checks is empty", where)
	}
	last := 0
	for _, ref := range g.Steps {
		n, verb, ok := parseStepRef(ref)
		if !ok {
			add("%s: step %q is not N:verb", where, ref)
			continue
		}
		if n <= last {
			add("%s: steps must be in increasing order (%q)", where, ref)
		}
		last = n
		if n < 1 || n > len(sc.Steps) {
			add("%s: step %d does not exist (%d steps)", where, n, len(sc.Steps))
			continue
		}
		if got := stepVerb(sc.Steps[n-1]); got != verb {
			add("%s: step %d is %q now, not %q", where, n, got, verb)
		}
	}
}

func parseStepRef(ref string) (int, string, bool) {
	num, verb, found := strings.Cut(ref, ":")
	n, err := strconv.Atoi(num)
	if !found || err != nil || verb == "" {
		return 0, "", false
	}
	return n, verb, true
}

// stepVerb is the request's action for a send step, the step kind otherwise.
func stepVerb(st harness.Step) string {
	if st.Do == "send" {
		var msg struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(st.Msg, &msg)
		return msg.Action
	}
	return st.Do
}

// StepNumbers returns the step numbers of a golden citation.
func (g Golden) StepNumbers() []int {
	out := make([]int, 0, len(g.Steps))
	for _, ref := range g.Steps {
		if n, _, ok := parseStepRef(ref); ok {
			out = append(out, n)
		}
	}
	return out
}
