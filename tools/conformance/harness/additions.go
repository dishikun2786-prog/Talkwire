package harness

import (
	"encoding/json"
	"sort"
)

// Additions are fields a server may add to the reference server's frames
// without changing anything else. Comparing a run with them stripped proves
// exactly that: the server answers like the reference, plus these fields.
type Additions struct {
	Name string
	// Keys are removed from every frame, at the top level.
	Keys []string
	// ReplyID removes the request id from the two replies the reference
	// server sends without one (finding F1): a successful login reply and a
	// presence snapshot reply.
	ReplyID bool
}

// KnownAdditions are the additions check -additions accepts.
var KnownAdditions = map[string]Additions{
	// The strict protocol profile (plan 1.7).
	"strict": {
		Name:    "strict",
		Keys:    []string{"errCode", "protocol", "capabilities", "vapidKey"},
		ReplyID: true,
	},
}

// AddedFrames are frames only ImKit's server sends, for behaviour the
// reference server does not have (marked 【测】 in the specification). They
// cannot have a golden case; the named test covers each instead.
var AddedFrames = map[string]string{
	"server custom_update": "server/custombroadcast_test.go",
}

// Strip removes the additions from the transcript's frames and counts what it
// removed, by field.
func (t *Transcript) Strip(a Additions) map[string]int {
	removed := map[string]int{}
	strip := func(frames map[string][]json.RawMessage) {
		for conn, list := range frames {
			out := make([]string, 0, len(list))
			for _, raw := range list {
				var m map[string]any
				if err := json.Unmarshal(raw, &m); err != nil {
					out = append(out, string(raw))
					continue
				}
				for _, k := range a.Keys {
					if _, ok := m[k]; ok {
						delete(m, k)
						removed[k]++
					}
				}
				if a.ReplyID && isIDlessReply(m) {
					if _, ok := m["id"]; ok {
						delete(m, "id")
						removed["id"]++
					}
				}
				out = append(out, string(canonical(m)))
			}
			sort.Strings(out)
			frames[conn] = frames[conn][:0]
			for _, s := range out {
				frames[conn] = append(frames[conn], json.RawMessage(s))
			}
		}
	}
	for i := range t.Steps {
		strip(t.Steps[i].Frames)
		strip(t.Steps[i].Optional)
	}
	return removed
}

// isIDlessReply reports whether a frame is one of the replies F1 describes.
func isIDlessReply(m map[string]any) bool {
	code, _ := m["code"].(float64)
	switch m["action"] {
	case "login":
		return code == 200
	case "pres":
		return code == 200 && m["what"] == "snapshot"
	}
	return false
}
