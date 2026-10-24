package replay

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"example.com/imkit/tools/conformance/harness"
)

// Placeholders a golden transcript uses, and the wildcards that stand for
// run-specific values.
var (
	placeholderPattern = regexp.MustCompile(`\$\{([^}]+)\}`)
	padPattern         = regexp.MustCompile(`^pad:(\d+)$`)
)

const (
	wildTime = "<time>"
	wildRev  = "<rev>"
)

// Bindings gives every placeholder in a scenario one concrete value, the same
// for the whole replay: ${uid:A} becomes A's real uid, ${grp} a group topic of
// the right shape, ${cs1} a call session id, and so on.
type Bindings struct {
	values map[string]string // placeholder body (e.g. "uid:A") -> value
}

// NewBindings collects every placeholder used by the scenario and transcript.
func NewBindings(sc *harness.Scenario, tr *harness.Transcript) *Bindings {
	kinds := map[string]string{} // captured variable -> reply field it came from
	for _, st := range sc.Steps {
		for name, path := range st.Capture {
			kinds[name] = path
		}
	}
	b := &Bindings{values: map[string]string{}}
	add := func(s string) {
		for _, m := range placeholderPattern.FindAllStringSubmatch(s, -1) {
			body := m[1]
			if _, done := b.values[body]; done || padPattern.MatchString(body) {
				continue
			}
			b.values[body] = valueFor(body, kinds)
		}
	}
	for _, st := range sc.Steps {
		add(string(st.Msg))
		add(st.Raw)
	}
	for _, rec := range tr.Steps {
		add(string(rec.Sent))
		for _, group := range []map[string][]json.RawMessage{rec.Frames, rec.Optional} {
			for _, frames := range group {
				for _, f := range frames {
					add(string(f))
				}
			}
		}
	}
	return b
}

func valueFor(body string, kinds map[string]string) string {
	switch {
	case strings.HasPrefix(body, "uid:"):
		return harness.IMUID(harness.AppUserID(strings.TrimPrefix(body, "uid:")))
	case strings.HasPrefix(body, "user:"):
		return harness.AppUserID(strings.TrimPrefix(body, "user:"))
	case strings.HasPrefix(body, "p2p:"):
		users := strings.SplitN(strings.TrimPrefix(body, "p2p:"), ",", 2)
		if len(users) == 2 {
			return harness.P2PTopic(harness.IMUID(harness.AppUserID(users[0])), harness.IMUID(harness.AppUserID(users[1])))
		}
	case strings.HasPrefix(body, "grp#"):
		return "grp_" + token(body, 12)
	}
	switch kinds[body] {
	case "topic":
		return "grp_" + token(body, 12)
	case "callSessionId":
		return token(body, 32)
	case "qrToken":
		return token(body, 24)
	}
	return token(body, 16)
}

// token derives a stable alphanumeric string of length n from seed, so two
// replays of the same scenario hand out the same ids.
func token(seed string, n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, 0, n)
	for i := 0; len(out) < n; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%s#%d", seed, i)))
		for _, c := range sum {
			if len(out) == n {
				break
			}
			out = append(out, alphabet[int(c)%len(alphabet)])
		}
	}
	return string(out)
}

// Value returns the concrete value of one placeholder body, e.g. "uid:A".
func (b *Bindings) Value(body string) (string, bool) {
	v, ok := b.values[body]
	return v, ok
}

// ExpandString replaces every placeholder in s. ${pad:N} becomes N "x".
func (b *Bindings) ExpandString(s string) string {
	return placeholderPattern.ReplaceAllStringFunc(s, func(m string) string {
		body := m[2 : len(m)-1]
		if p := padPattern.FindStringSubmatch(body); p != nil {
			n, _ := strconv.Atoi(p[1])
			return strings.Repeat("x", n)
		}
		if v, ok := b.values[body]; ok {
			return v
		}
		return m
	})
}

// Expand replaces placeholders in every string of a decoded JSON value. The
// wildcards <time> and <rev> are left as they are.
func (b *Bindings) Expand(v any) any {
	switch x := v.(type) {
	case string:
		return b.ExpandString(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = b.Expand(e)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, e := range x {
			out[k] = b.Expand(e)
		}
		return out
	}
	return v
}

// corrKeys are the top-level fields whose values a client chooses itself. A
// replay maps the recorded value to whatever the client actually sent, one to
// one, and writes the client's value back into the frames it emits.
var corrKeys = []string{"id", "reqId", "clientMsgId", "clientCallId"}

// Correlator remembers, per correlation field, which client value stands for
// which recorded value.
type Correlator struct {
	toActual map[string]map[string]string
	toRecord map[string]map[string]string
}

func newCorrelator() *Correlator {
	c := &Correlator{toActual: map[string]map[string]string{}, toRecord: map[string]map[string]string{}}
	for _, k := range corrKeys {
		c.toActual[k] = map[string]string{}
		c.toRecord[k] = map[string]string{}
	}
	return c
}

// bind records that the client used actual where the transcript has recorded.
// The same recorded value must always map to the same client value. Request
// ids (id, reqId) may be reused by the client once a request is answered, so
// only message and call ids must also be unique on the client side: two
// distinct recorded messages sharing one clientMsgId could not be told apart.
func (c *Correlator) bind(key, recorded, actual string) error {
	if err := c.check(key, recorded, actual); err != nil {
		return err
	}
	c.toActual[key][recorded] = actual
	c.toRecord[key][actual] = recorded
	return nil
}

// check reports whether bind would accept the pair, without changing anything.
func (c *Correlator) check(key, recorded, actual string) error {
	if prev, ok := c.toActual[key][recorded]; ok {
		if prev != actual {
			return fmt.Errorf("%s: recorded %q was sent as %q before, now %q", key, recorded, prev, actual)
		}
		return nil
	}
	reusable := key == "id" || key == "reqId"
	if prev, ok := c.toRecord[key][actual]; ok && !reusable && actual != "" && prev != recorded {
		return fmt.Errorf("%s: client reused %q for both %q and %q", key, actual, prev, recorded)
	}
	return nil
}

// rewrite replaces recorded correlation values in an outgoing frame with the
// client's. A request sent without an id gets replies without one.
func (c *Correlator) rewrite(frame map[string]any) {
	for _, k := range corrKeys {
		s, ok := frame[k].(string)
		if !ok {
			continue
		}
		if actual, bound := c.toActual[k][s]; bound {
			if actual == "" {
				delete(frame, k)
			} else {
				frame[k] = actual
			}
		}
	}
}

// matchRequest checks a client frame against the expected one. expected has
// placeholders expanded already. strictID is set when the scenario itself fixed
// the id (it then carries meaning, e.g. ack_topic_cleared's row id). Fields the
// client adds beyond the expected ones are not checked. Only a matching
// request changes the correspondence, so a rejected frame leaves no trace.
func matchRequest(expected, actual map[string]any, replyID string, strictID bool, c *Correlator) error {
	var problems []string
	var pending [][3]string // key, recorded, actual
	for k, want := range expected {
		got, present := actual[k]
		isCorr := false
		for _, ck := range corrKeys {
			if k == ck && !(k == "id" && strictID) {
				isCorr = true
			}
		}
		if isCorr {
			ws, _ := want.(string)
			gs, _ := got.(string)
			pending = append(pending, [3]string{k, ws, gs})
			continue
		}
		if !present {
			problems = append(problems, fmt.Sprintf("missing %q", k))
			continue
		}
		if !reflect.DeepEqual(want, got) {
			problems = append(problems, fmt.Sprintf("%q: want %s, got %s", k, short(want), short(got)))
		}
	}
	if !strictID {
		gs, _ := actual["id"].(string)
		pending = append(pending, [3]string{"id", replyID, gs})
	}
	for _, p := range pending {
		if err := c.check(p[0], p[1], p[2]); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	for _, p := range pending {
		if err := c.bind(p[0], p[1], p[2]); err != nil {
			return err
		}
	}
	return nil
}

// MatchFrame reports whether actual equals expected, where expected may hold
// the wildcards <time> and <rev> (any number matches).
func MatchFrame(expected, actual any) bool {
	switch e := expected.(type) {
	case string:
		if e == wildTime || e == wildRev {
			_, ok := actual.(float64)
			return ok
		}
		return e == actual
	case map[string]any:
		a, ok := actual.(map[string]any)
		if !ok || len(a) != len(e) {
			return false
		}
		for k, ev := range e {
			av, ok := a[k]
			if !ok || !MatchFrame(ev, av) {
				return false
			}
		}
		return true
	case []any:
		a, ok := actual.([]any)
		if !ok || len(a) != len(e) {
			return false
		}
		for i := range e {
			if !MatchFrame(e[i], a[i]) {
				return false
			}
		}
		return true
	}
	return reflect.DeepEqual(expected, actual)
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 80 {
		return string(b[:77]) + "..."
	}
	return string(b)
}

// Keys lists every placeholder body this replay binds.
func (b *Bindings) Keys() []string {
	out := make([]string, 0, len(b.values))
	for k := range b.values {
		out = append(out, k)
	}
	return out
}

// TranscriptFile is the golden file name for a scenario ("core/p2p" ->
// "core__p2p.json").
func TranscriptFile(scenario string) string {
	return strings.ReplaceAll(scenario, "/", "__") + ".json"
}
