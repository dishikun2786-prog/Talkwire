package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Vocabulary is every frame name the protocol defines: the actions a client
// may send, and the action and type names a server may send. It is read from
// the two JSON Schemas in contract/protocol/schema, which are the contract.
type Vocabulary struct {
	Client []string // request actions
	Server []string // server frame actions and types
}

// LoadVocabulary reads the frame names from client-frames.schema.json and
// server-frames.schema.json in dir.
func LoadVocabulary(dir string) (*Vocabulary, error) {
	var client struct {
		Properties struct {
			Action struct {
				Enum []string `json:"enum"`
			} `json:"action"`
		} `json:"properties"`
	}
	if err := readJSON(filepath.Join(dir, "client-frames.schema.json"), &client); err != nil {
		return nil, err
	}
	var server struct {
		AnyOf []struct {
			Properties map[string]struct {
				Enum  []string `json:"enum"`
				Const string   `json:"const"`
			} `json:"properties"`
		} `json:"anyOf"`
	}
	if err := readJSON(filepath.Join(dir, "server-frames.schema.json"), &server); err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, branch := range server.AnyOf {
		for _, key := range []string{"action", "type"} {
			p := branch.Properties[key]
			for _, n := range p.Enum {
				names[n] = true
			}
			if p.Const != "" {
				names[p.Const] = true
			}
		}
	}
	v := &Vocabulary{Client: append([]string(nil), client.Properties.Action.Enum...), Server: keys(names)}
	sort.Strings(v.Client)
	if len(v.Client) == 0 || len(v.Server) == 0 {
		return nil, fmt.Errorf("%s: no frame names found in the schemas", dir)
	}
	return v, nil
}

// StepSet names steps to leave out, by scenario and 1-based step number.
type StepSet map[string]map[int]bool

// LoadStepSet reads a {"scenario": [step, ...]} file such as
// tools/schema/negative-cases.json: steps that deliberately send an invalid
// request and so must not count as covering anything.
func LoadStepSet(path string) (StepSet, error) {
	var raw map[string][]int
	if err := readJSON(path, &raw); err != nil {
		return nil, err
	}
	set := StepSet{}
	for name, steps := range raw {
		set[name] = map[int]bool{}
		for _, n := range steps {
			set[name][n] = true
		}
	}
	return set, nil
}

func readJSON(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// eachName calls fn for every frame name in the golden set: the action of
// each login and send step (side "client") and the action and type of each
// recorded frame (side "server"). Optional frames are left out because they
// are not reliably delivered, and so are the steps in skip.
func eachName(scs []*Scenario, trs map[string]*Transcript, skip StepSet, fn func(side, name, scenario string)) {
	for _, sc := range scs {
		for i, st := range sc.Steps {
			if skip[sc.Name][i+1] {
				continue
			}
			switch st.Do {
			case "login":
				fn("client", "login", sc.Name)
			case "send":
				var msg struct {
					Action string `json:"action"`
				}
				if json.Unmarshal(st.Msg, &msg) == nil && msg.Action != "" {
					fn("client", msg.Action, sc.Name)
				}
			}
		}
		tr := trs[sc.Name]
		if tr == nil {
			continue
		}
		for i, rec := range tr.Steps {
			if skip[sc.Name][i+1] {
				continue
			}
			for _, frames := range rec.Frames {
				for _, raw := range frames {
					var f struct {
						Action string `json:"action"`
						Type   string `json:"type"`
					}
					if json.Unmarshal(raw, &f) != nil {
						continue
					}
					if f.Action != "" {
						fn("server", f.Action, sc.Name)
					}
					if f.Type != "" && f.Type != f.Action {
						fn("server", f.Type, sc.Name)
					}
				}
			}
		}
	}
}

// Use is how often one frame name occurs in the golden set, and where.
type Use struct {
	Side  string   // "client" or "server"
	Name  string   // action or type
	Count int      // send/login steps (client) or recorded frames (server)
	Where []string // scenario names, sorted, each once
}

// Coverage counts each vocabulary name in the scenarios (what clients send)
// and the transcripts (what the reference server sent).
func Coverage(v *Vocabulary, scs []*Scenario, trs map[string]*Transcript, skip StepSet) []Use {
	count := map[string]int{}
	where := map[string]map[string]bool{}
	eachName(scs, trs, skip, func(side, name, scenario string) {
		k := side + " " + name
		count[k]++
		if where[k] == nil {
			where[k] = map[string]bool{}
		}
		where[k][scenario] = true
	})
	var out []Use
	for _, side := range []struct {
		name  string
		names []string
	}{{"client", v.Client}, {"server", v.Server}} {
		for _, n := range side.names {
			k := side.name + " " + n
			out = append(out, Use{Side: side.name, Name: n, Count: count[k], Where: keys(where[k])})
		}
	}
	return out
}

// Unlisted returns names the golden set uses but the schemas do not define
// ("client x" or "server x"): a sign the schemas are out of date.
func Unlisted(v *Vocabulary, scs []*Scenario, trs map[string]*Transcript, skip StepSet) []string {
	known := map[string]bool{}
	for _, n := range v.Client {
		known["client "+n] = true
	}
	for _, n := range v.Server {
		known["server "+n] = true
	}
	found := map[string]bool{}
	eachName(scs, trs, skip, func(side, name, _ string) {
		if !known[side+" "+name] {
			found[side+" "+name] = true
		}
	})
	return keys(found)
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sourceConstPattern matches Action* and Type* string constants in the
// reference server's Go source.
var sourceConstPattern = regexp.MustCompile(`(?m)^\s*((?:Action|Type)[A-Za-z0-9]+)\s*=\s*"([^"]+)"`)

// SourceConstants extracts the Action* and Type* constants (name -> value)
// from the given Go files of the reference server.
func SourceConstants(files ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		for _, m := range sourceConstPattern.FindAllStringSubmatch(string(raw), -1) {
			out[m[1]] = m[2]
		}
	}
	return out, nil
}
