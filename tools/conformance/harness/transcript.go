package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/dishikun2786-prog/Talkwire/tools/conformance/fakehost"
)

// Transcript is what a scenario produced, after normalization.
type Transcript struct {
	Scenario string       `json:"scenario"`
	Source   string       `json:"source"`
	Steps    []StepRecord `json:"steps"`

	vars map[string]string
}

// StepRecord is everything observed during one step and its settle window.
type StepRecord struct {
	Index        int                          `json:"index"`
	Do           string                       `json:"do"`
	Conn         string                       `json:"conn,omitempty"`
	Note         string                       `json:"note,omitempty"`
	Sent         json.RawMessage              `json:"sent,omitempty"`
	TimedOut     bool                         `json:"timedOut,omitempty"`
	Frames       map[string][]json.RawMessage `json:"frames,omitempty"`
	Optional     map[string][]json.RawMessage `json:"optional,omitempty"`
	Closed       []string                     `json:"closed,omitempty"`
	HostCalls    []json.RawMessage            `json:"hostCalls,omitempty"`
	HostResponse any                          `json:"hostResponse,omitempty"`

	raw       map[string][]json.RawMessage
	hostCalls []fakehost.Call
}

// Keys whose values depend on the wall clock or on process start time.
var clockKeys = map[string]bool{
	"ts": true, "createdAt": true, "updatedAt": true, "lastSeen": true, "lastSeenAt": true,
	"lastSeenMs": true, "expiresAt": true, "clearedAt": true, "joinedAt": true, "timestamp": true,
	"sentAt": true, "at": true, "reviewedAt": true,
}

// Keys that are monotonic counters seeded from the clock.
var revKeys = map[string]bool{"rev": true, "presRev": true}

// Keys holding random server-minted ids. A value a scenario captured keeps its
// named placeholder; any other value (e.g. the id of a call the host refused,
// which no reply ever exposes) becomes this fixed placeholder.
var randomIDKeys = map[string]string{"callSessionId": "<call-id>"}

var grpPattern = regexp.MustCompile(`grp_[A-Za-z0-9]{12}`)

type normalizer struct {
	exact   map[string]string
	partial []string // keys of exact usable as substrings, longest first
	grp     map[string]string
}

func newNormalizer(users []string, vars map[string]string) *normalizer {
	n := &normalizer{exact: map[string]string{}, grp: map[string]string{}}
	for _, u := range users {
		n.exact[AppUserID(u)] = "${user:" + u + "}"
		n.exact[IMUID(AppUserID(u))] = "${uid:" + u + "}"
	}
	for i, a := range users {
		for _, b := range users[i+1:] {
			n.exact[P2PTopic(IMUID(AppUserID(a)), IMUID(AppUserID(b)))] = "${p2p:" + a + "," + b + "}"
		}
	}
	for k := range n.exact {
		n.partial = append(n.partial, k)
	}
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		v := vars[k]
		// Only long, id-like captured values are rewritten; a captured "1"
		// must not turn every 1 in the transcript into a placeholder.
		if len(v) >= 6 {
			n.exact[v] = "${" + k + "}"
			n.partial = append(n.partial, v)
		}
	}
	sort.Slice(n.partial, func(i, j int) bool { return len(n.partial[i]) > len(n.partial[j]) })
	return n
}

func (n *normalizer) isUncaptured(v any) bool {
	s, ok := v.(string)
	if !ok || s == "" {
		return false
	}
	_, known := n.exact[s]
	return !known
}

func (n *normalizer) value(key string, v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			switch {
			case clockKeys[k] && vv != nil && vv != "" && vv != float64(0):
				out[k] = "<time>"
			case revKeys[k] && vv != nil:
				out[k] = "<rev>"
			case randomIDKeys[k] != "" && n.isUncaptured(vv):
				out[k] = randomIDKeys[k]
			default:
				out[k] = n.value(k, vv)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = n.value(key, vv)
		}
		return out
	case string:
		if p, ok := n.exact[x]; ok {
			return p
		}
		s := x
		for _, k := range n.partial {
			if strings.Contains(s, k) {
				s = strings.ReplaceAll(s, k, n.exact[k])
			}
		}
		return grpPattern.ReplaceAllStringFunc(s, func(g string) string {
			if p, ok := n.grp[g]; ok {
				return p
			}
			p := fmt.Sprintf("${grp#%d}", len(n.grp)+1)
			n.grp[g] = p
			return p
		})
	}
	return v
}

// marshal encodes without HTML escaping so placeholders such as <time> stay
// readable in golden files and diffs.
func marshal(v any, indent bool) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent {
		enc.SetIndent("", "  ")
	}
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

func canonical(v any) json.RawMessage {
	return marshal(v, false)
}

// sortUnordered sorts arrays whose order the spec leaves open (protocol-v1.md
// §10.9: members who joined in the same instant have no defined order), so a
// different but valid order does not fail a comparison. It runs after
// placeholder substitution, so the result does not depend on real ids.
func sortUnordered(v any) {
	m, ok := v.(map[string]any)
	if !ok || m["action"] != "get_members" {
		return
	}
	members, ok := m["members"].([]any)
	if !ok {
		return
	}
	sort.SliceStable(members, func(i, j int) bool {
		return string(canonical(members[i])) < string(canonical(members[j]))
	})
}

// Normalize rewrites run-specific values into placeholders and sorts frames,
// so two runs of the same scenario against equivalent servers compare equal.
func (t *Transcript) Normalize(sc *Scenario) {
	n := newNormalizer(sc.Users, t.vars)
	for i := range t.Steps {
		s := &t.Steps[i]
		if len(s.raw) > 0 {
			var step Step
			if i < len(sc.Steps) {
				step = sc.Steps[i]
			}
			s.Frames = map[string][]json.RawMessage{}
			for conn, frames := range s.raw {
				var list, optional []string
				for _, raw := range frames {
					var v any
					if err := json.Unmarshal(raw, &v); err != nil {
						list = append(list, string(canonical(map[string]any{"unparseable": string(raw)})))
						continue
					}
					nv := n.value("", v)
					sortUnordered(nv)
					if step.optionalMatch(conn, nv) {
						optional = append(optional, string(canonical(nv)))
						continue
					}
					list = append(list, string(canonical(nv)))
				}
				sort.Strings(list)
				for _, item := range list {
					s.Frames[conn] = append(s.Frames[conn], json.RawMessage(item))
				}
				if len(optional) > 0 {
					if s.Optional == nil {
						s.Optional = map[string][]json.RawMessage{}
					}
					sort.Strings(optional)
					for _, item := range optional {
						s.Optional[conn] = append(s.Optional[conn], json.RawMessage(item))
					}
				}
			}
			if len(s.Frames) == 0 {
				s.Frames = nil
			}
		}
		var calls []string
		for _, c := range s.hostCalls {
			var v any
			_ = json.Unmarshal(canonical(c), &v)
			calls = append(calls, string(canonical(n.value("", v))))
		}
		sort.Strings(calls)
		for _, c := range calls {
			s.HostCalls = append(s.HostCalls, json.RawMessage(c))
		}
		if s.HostResponse != nil {
			s.HostResponse = n.value("", s.HostResponse)
		}
		sort.Strings(s.Closed)
	}
}

// Write stores the transcript as indented JSON.
func (t *Transcript) Write(path string) error {
	return os.WriteFile(path, append(marshal(t, true), '\n'), 0o644)
}

// ReadTranscript loads a golden transcript.
func ReadTranscript(path string) (*Transcript, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Transcript
	if err := json.Unmarshal(raw, &t); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &t, nil
}

// Diff is one difference between a golden and an observed transcript.
type Diff struct {
	Step  int
	Where string
	Want  string
	Got   string
}

func (d Diff) String() string {
	return fmt.Sprintf("step %d %s\n    want: %s\n    got:  %s", d.Step, d.Where, d.Want, d.Got)
}

// compact renders v as compact JSON; encoding/json also compacts embedded
// RawMessage values, so indented golden files compare equal to fresh runs.
func compact(v any) string {
	return string(marshal(v, false))
}

// Compare returns the differences between want and got (source labels and
// notes are ignored).
func Compare(want, got *Transcript) []Diff {
	var diffs []Diff
	if len(want.Steps) != len(got.Steps) {
		return []Diff{{Where: "step count", Want: fmt.Sprint(len(want.Steps)), Got: fmt.Sprint(len(got.Steps))}}
	}
	for i := range want.Steps {
		w, g := want.Steps[i], got.Steps[i]
		if w.TimedOut != g.TimedOut {
			diffs = append(diffs, Diff{w.Index, "timedOut", fmt.Sprint(w.TimedOut), fmt.Sprint(g.TimedOut)})
		}
		conns := map[string]bool{}
		for c := range w.Frames {
			conns[c] = true
		}
		for c := range g.Frames {
			conns[c] = true
		}
		names := make([]string, 0, len(conns))
		for c := range conns {
			names = append(names, c)
		}
		sort.Strings(names)
		for _, c := range names {
			if a, b := compact(w.Frames[c]), compact(g.Frames[c]); a != b {
				diffs = append(diffs, Diff{w.Index, "frames on " + c, a, b})
			}
		}
		if a, b := compact(w.Closed), compact(g.Closed); a != b {
			diffs = append(diffs, Diff{w.Index, "closed", a, b})
		}
		if a, b := compact(w.HostCalls), compact(g.HostCalls); a != b {
			diffs = append(diffs, Diff{w.Index, "host calls", a, b})
		}
		if a, b := compact(w.HostResponse), compact(g.HostResponse); a != b {
			diffs = append(diffs, Diff{w.Index, "host response", a, b})
		}
	}
	return diffs
}
