package replay

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"example.com/imkit/tools/conformance/harness"
)

const (
	scenarioDir   = "../../../contract/protocol/scenarios"
	transcriptDir = "../../../contract/protocol/transcripts"
	readTimeout   = 3 * time.Second
)

func loadAll(t *testing.T) ([]*harness.Scenario, map[string]*harness.Transcript) {
	t.Helper()
	scs, err := harness.LoadScenarios(scenarioDir)
	if err != nil {
		t.Fatal(err)
	}
	trs := map[string]*harness.Transcript{}
	for _, sc := range scs {
		tr, err := harness.ReadTranscript(filepath.Join(transcriptDir, TranscriptFile(sc.Name)))
		if err != nil {
			t.Fatal(err)
		}
		trs[sc.Name] = tr
	}
	return scs, trs
}

func loadOne(t *testing.T, name string) (*harness.Scenario, *harness.Transcript) {
	scs, trs := loadAll(t)
	for _, sc := range scs {
		if sc.Name == name {
			return sc, trs[name]
		}
	}
	t.Fatalf("no scenario %s", name)
	return nil, nil
}

// scriptClient is a well-behaved client that follows the scenario, so the
// replay's own behaviour can be checked: it uses its own request ids
// ("cli-<step>") and its own message ids ("cli-" + recorded), and checks that
// every frame it receives is the recorded one with those ids substituted.
type scriptClient struct {
	t      *testing.T
	url    string
	sc     *harness.Scenario
	tr     *harness.Transcript
	b      *Bindings
	opt    Options
	conns  map[string]*clientConn
	sentBy map[string]map[string]bool // corr key -> recorded values this client sent
}

type clientConn struct {
	ws     *websocket.Conn
	frames chan []byte
	done   chan struct{}
}

func newScriptClient(t *testing.T, url string, srv *Server, sc *harness.Scenario, tr *harness.Transcript, opt Options) *scriptClient {
	c := &scriptClient{t: t, url: url, sc: sc, tr: tr, b: srv.Bindings(), opt: opt, conns: map[string]*clientConn{}, sentBy: map[string]map[string]bool{}}
	for _, k := range corrKeys {
		c.sentBy[k] = map[string]bool{}
	}
	return c
}

func (c *scriptClient) dial(name string) error {
	ws, _, err := websocket.DefaultDialer.Dial(c.url, nil)
	if err != nil {
		return err
	}
	cc := &clientConn{ws: ws, frames: make(chan []byte, 1024), done: make(chan struct{})}
	go func() {
		defer close(cc.done)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			cc.frames <- data
		}
	}()
	c.conns[name] = cc
	return nil
}

func (c *scriptClient) subject(conn string) bool {
	for _, n := range c.opt.Conns {
		if n == conn {
			return true
		}
	}
	return false
}

func (c *scriptClient) send(conn string, v any) error {
	data, _ := json.Marshal(v)
	return c.conns[conn].ws.WriteMessage(websocket.TextMessage, data)
}

func (c *scriptClient) read(conn string) (map[string]any, error) {
	cc := c.conns[conn]
	select {
	case data := <-cc.frames:
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		return m, nil
	case <-cc.done:
		select {
		case data := <-cc.frames:
			var m map[string]any
			err := json.Unmarshal(data, &m)
			return m, err
		default:
		}
		return nil, fmt.Errorf("%s: connection closed", conn)
	case <-time.After(readTimeout):
		return nil, fmt.Errorf("%s: timed out", conn)
	}
}

func (c *scriptClient) waitClosed(conn string) error {
	cc := c.conns[conn]
	select {
	case <-cc.done:
		// Frames the server sent before closing were already accounted for.
		return nil
	case <-time.After(readTimeout):
		return fmt.Errorf("%s: server did not close the connection", conn)
	}
}

// expected turns a recorded frame into what this client should receive.
func (c *scriptClient) expected(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	v = c.b.Expand(v)
	if m, ok := v.(map[string]any); ok {
		for _, k := range corrKeys {
			if s, ok := m[k].(string); ok && c.sentBy[k][s] {
				m[k] = clientValue(k, s)
			}
		}
	}
	return v
}

func clientValue(key, recorded string) string {
	if key == "id" {
		return "cli-" + strings.TrimPrefix(recorded, "s")
	}
	return "cli-" + recorded
}

type faultSet struct {
	disconnect, drop, duplicate map[int]bool
	delay                       map[int]int
	resub                       bool // send an unscripted sub after reconnecting
}

func (f faultSet) at(kind string, i int) bool {
	switch kind {
	case "disconnect":
		return f.disconnect[i]
	case "drop":
		return f.drop[i]
	case "duplicate":
		return f.duplicate[i]
	}
	return false
}

// run plays the whole scenario. order, if set, records the frames each step
// delivered to its own connection, in arrival order.
func (c *scriptClient) run(f faultSet, order map[int][]map[string]any) error {
	for i, st := range c.sc.Steps {
		idx := i + 1
		rec := c.tr.Steps[i]
		if f.at("disconnect", idx) {
			if err := c.reconnect(idx, f.resub); err != nil {
				return fmt.Errorf("step %d: %w", idx, err)
			}
		}
		start := time.Now()
		if err := c.act(idx, st); err != nil {
			return fmt.Errorf("step %d (%s %s): %w", idx, st.Do, st.Conn, err)
		}
		if err := c.receive(idx, st, rec, f, start, order); err != nil {
			return fmt.Errorf("step %d (%s %s): %w", idx, st.Do, st.Conn, err)
		}
	}
	// Nothing more should arrive.
	time.Sleep(50 * time.Millisecond)
	for name, cc := range c.conns {
		select {
		case data := <-cc.frames:
			return fmt.Errorf("extra frame on %s: %s", name, data)
		default:
		}
	}
	return nil
}

func (c *scriptClient) act(idx int, st harness.Step) error {
	if !c.subject(st.Conn) {
		return nil
	}
	switch st.Do {
	case "connect":
		return c.dial(st.Conn)
	case "close":
		cc := c.conns[st.Conn]
		_ = cc.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		<-cc.done
		_ = cc.ws.Close()
		delete(c.conns, st.Conn)
	case "login":
		c.sentBy["id"][fmt.Sprintf("s%d", idx)] = true
		return c.send(st.Conn, map[string]any{"action": "login", "id": fmt.Sprintf("cli-%d", idx), "token": "replay-test"})
	case "raw":
		// Gorilla writes big frames in fragments; the replay may close first.
		_ = c.conns[st.Conn].ws.WriteMessage(websocket.TextMessage, []byte(c.b.ExpandString(st.Raw)))
	case "send":
		var msg map[string]any
		if err := json.Unmarshal([]byte(c.b.ExpandString(string(st.Msg))), &msg); err != nil {
			return err
		}
		if _, has := msg["id"]; !has {
			c.sentBy["id"][fmt.Sprintf("s%d", idx)] = true
			msg["id"] = fmt.Sprintf("cli-%d", idx)
		}
		for _, k := range []string{"reqId", "clientMsgId", "clientCallId"} {
			if s, ok := msg[k].(string); ok {
				c.sentBy[k][s] = true
				msg[k] = clientValue(k, s)
			}
		}
		return c.send(st.Conn, msg)
	}
	return nil
}

func (c *scriptClient) receive(idx int, st harness.Step, rec harness.StepRecord, f faultSet, start time.Time, order map[int][]map[string]any) error {
	var names []string
	for name := range c.conns {
		if c.subject(name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		want := append([]json.RawMessage{}, rec.Frames[name]...)
		if !c.opt.SkipOptional {
			want = append(want, rec.Optional[name]...)
		}
		if f.drop[idx] {
			want = nil
		}
		if f.duplicate[idx] {
			want = append(want, want...)
		}
		exp := make([]any, len(want))
		for i, raw := range want {
			exp[i] = c.expected(raw)
		}
		used := make([]bool, len(exp))
		for n := 0; n < len(exp); n++ {
			got, err := c.read(name)
			if err != nil {
				return fmt.Errorf("frame %d of %d: %w", n+1, len(exp), err)
			}
			if n == 0 && f.delay[idx] > 0 {
				if el := time.Since(start); el < time.Duration(f.delay[idx])*time.Millisecond {
					return fmt.Errorf("first frame after %s, delay was %dms", el, f.delay[idx])
				}
			}
			if order != nil && name == st.Conn {
				order[idx] = append(order[idx], got)
			}
			found := false
			for j := range exp {
				if !used[j] && MatchFrame(exp[j], any(got)) {
					used[j], found = true, true
					break
				}
			}
			if !found {
				g, _ := json.Marshal(got)
				e, _ := json.Marshal(exp)
				return fmt.Errorf("unexpected frame on %s: %s\n  expected one of %s", name, g, e)
			}
		}
	}
	for _, name := range rec.Closed {
		if c.subject(name) && c.conns[name] != nil {
			if err := c.waitClosed(name); err != nil {
				return err
			}
			delete(c.conns, name)
		}
	}
	return nil
}

func (c *scriptClient) reconnect(idx int, resub bool) error {
	var names []string
	for name := range c.conns {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := c.waitClosed(name); err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := c.dial(name); err != nil {
			return err
		}
		if err := c.send(name, map[string]any{"action": "login", "id": "relogin", "token": "replay-test"}); err != nil {
			return err
		}
		ack, err := c.read(name)
		if err != nil {
			return err
		}
		user := ""
		for _, st := range c.sc.Steps {
			if st.Do == "login" && st.Conn == name {
				user = st.User
			}
		}
		uid, _ := c.b.Value("uid:" + user)
		if ack["action"] != "login" || ack["code"] != float64(200) || ack["uid"] != uid || ack["id"] != nil {
			return fmt.Errorf("bad login ack after reconnect: %v", ack)
		}
		if resub {
			peer, _ := c.b.Value("uid:B")
			if err := c.send(name, map[string]any{"action": "sub", "id": "resub", "topic": peer}); err != nil {
				return err
			}
			r, err := c.read(name)
			if err != nil {
				return err
			}
			if r["action"] != "sub" || r["code"] != float64(200) || r["id"] != "resub" || r["topic"] != peer {
				return fmt.Errorf("bad resubscribe reply: %v", r)
			}
		}
	}
	return nil
}

type replayRun struct {
	rep   *Report
	err   error
	order map[int][]map[string]any
}

func runReplay(t *testing.T, sc *harness.Scenario, tr *harness.Transcript, opt Options, f faultSet) replayRun {
	t.Helper()
	srv, err := New(sc, tr, opt)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/"
	client := newScriptClient(t, url, srv, sc, tr, opt)
	done := make(chan *Report, 1)
	go func() { done <- srv.Run() }()
	order := map[int][]map[string]any{}
	cerr := client.run(f, order)
	for _, cc := range client.conns {
		_ = cc.ws.Close()
	}
	var rep *Report
	select {
	case rep = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("replay did not finish")
	}
	return replayRun{rep: rep, err: cerr, order: order}
}

func (r replayRun) check(t *testing.T) {
	t.Helper()
	if r.err != nil {
		t.Errorf("client: %v", r.err)
	}
	if !r.rep.OK() {
		t.Errorf("replay: %d/%d steps, errors: %v", r.rep.Completed, r.rep.Steps, r.rep.Errors)
	}
}

// TestGoldens replays every golden transcript once per scenario user, with a
// client that follows the script exactly.
func TestGoldens(t *testing.T) {
	scs, trs := loadAll(t)
	if len(scs) == 0 {
		t.Fatal("no scenarios")
	}
	runs := 0
	for _, sc := range scs {
		for _, user := range sc.Users {
			conns := ConnsForUser(sc, user)
			if len(conns) == 0 {
				continue
			}
			sc, user := sc, user
			t.Run(sc.Name+"/"+user, func(t *testing.T) {
				r := runReplay(t, sc, trs[sc.Name], Options{Conns: conns, Timeout: 3 * time.Second, Settle: 20 * time.Millisecond}, faultSet{})
				r.check(t)
			})
			runs++
		}
	}
	t.Logf("%d scenarios, %d replays", len(scs), runs)
}

// TestAllConns plays every connection at once: every recorded frame is sent.
func TestAllConns(t *testing.T) {
	scs, trs := loadAll(t)
	for _, sc := range scs {
		var conns []string
		seen := map[string]bool{}
		for _, st := range sc.Steps {
			if st.Do == "connect" && !seen[st.Conn] {
				seen[st.Conn] = true
				conns = append(conns, st.Conn)
			}
		}
		sc := sc
		t.Run(sc.Name, func(t *testing.T) {
			runReplay(t, sc, trs[sc.Name], Options{Conns: conns, Timeout: 3 * time.Second, Settle: 20 * time.Millisecond}, faultSet{}).check(t)
		})
	}
}

func p2pOptions(faults ...Fault) Options {
	return Options{Conns: []string{"a"}, Timeout: 3 * time.Second, Settle: 20 * time.Millisecond, Faults: faults}
}

// Step 9 of core/p2p is A's pub; step 7 is A's sub (reply plus a presence
// snapshot); step 11 is A's presence query.

func TestFaultDisconnect(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	r := runReplay(t, sc, tr, p2pOptions(Fault{Kind: "disconnect", Step: 9}), faultSet{disconnect: map[int]bool{9: true}, resub: true})
	r.check(t)
	joined := strings.Join(r.rep.Events, "\n")
	for _, want := range []string{"disconnected a", "a reconnected and logged in", "a resubscribed"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing event %q in %v", want, r.rep.Events)
		}
	}
}

func TestFaultDisconnectWithoutLogin(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	srv, err := New(sc, tr, p2pOptions(Fault{Kind: "disconnect", Step: 9}))
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/"
	client := newScriptClient(t, url, srv, sc, tr, srv.opt)
	done := make(chan *Report, 1)
	go func() { done <- srv.Run() }()
	for i := 0; i < 8; i++ { // steps 1-8, then reconnect and send a pub instead of logging in
		if err := client.act(i+1, sc.Steps[i]); err != nil {
			t.Fatal(err)
		}
		if err := client.receive(i+1, sc.Steps[i], tr.Steps[i], faultSet{}, time.Now(), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.waitClosed("a"); err != nil {
		t.Fatal(err)
	}
	if err := client.dial("a"); err != nil {
		t.Fatal(err)
	}
	_ = client.send("a", map[string]any{"action": "pub", "topic": "x", "content": "too early"})
	rep := <-done
	if rep.OK() || len(rep.Errors) == 0 || !strings.Contains(rep.Errors[0], "must log in first") {
		t.Errorf("want a must-log-in error, got %v", rep.Errors)
	}
}

func TestFaultDuplicate(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	r := runReplay(t, sc, tr, p2pOptions(Fault{Kind: "duplicate", Step: 7}), faultSet{duplicate: map[int]bool{7: true}})
	r.check(t)
	if len(r.order[7]) != 4 {
		t.Errorf("step 7: want 4 frames (2 recorded, duplicated), got %d", len(r.order[7]))
	}
}

func TestFaultDuplicateEveryStep(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	dup := map[int]bool{}
	for i := range sc.Steps {
		dup[i+1] = true
	}
	runReplay(t, sc, tr, p2pOptions(Fault{Kind: "duplicate", Step: 0}), faultSet{duplicate: dup}).check(t)
}

func TestFaultDrop(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	r := runReplay(t, sc, tr, p2pOptions(Fault{Kind: "drop", Step: 9}), faultSet{drop: map[int]bool{9: true}})
	r.check(t)
	if len(r.order[9]) != 0 {
		t.Errorf("step 9: frames were not dropped: %v", r.order[9])
	}
	if !strings.Contains(strings.Join(r.rep.Events, "\n"), "dropped 1 frame(s) for a") {
		t.Errorf("no drop event: %v", r.rep.Events)
	}
}

func TestFaultDelay(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	runReplay(t, sc, tr, p2pOptions(Fault{Kind: "delay", Step: 11, Ms: 300}), faultSet{delay: map[int]int{11: 300}}).check(t)
}

func TestReplyOrder(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	for _, order := range []string{"first", "last"} {
		opt := p2pOptions()
		opt.Order = order
		r := runReplay(t, sc, tr, opt, faultSet{})
		r.check(t)
		frames := r.order[7]
		if len(frames) != 2 {
			t.Fatalf("%s: step 7 delivered %d frames", order, len(frames))
		}
		replyAt := 0
		if order == "last" {
			replyAt = 1
		}
		if frames[replyAt]["action"] != "sub" || frames[replyAt]["id"] != "cli-7" {
			t.Errorf("%s: reply not at position %d: %v", order, replyAt, frames)
		}
	}
}

// A client that sends the wrong thing must fail the replay.
func TestWrongRequestFails(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	srv, err := New(sc, tr, p2pOptions())
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/"
	client := newScriptClient(t, url, srv, sc, tr, srv.opt)
	done := make(chan *Report, 1)
	go func() { done <- srv.Run() }()
	for i := 0; i < 8; i++ {
		_ = client.act(i+1, sc.Steps[i])
		_ = client.receive(i+1, sc.Steps[i], tr.Steps[i], faultSet{}, time.Now(), nil)
	}
	_ = client.send("a", map[string]any{"action": "pub", "id": "x", "topic": "uid_wrong", "content": "hey", "contentType": "text/plain", "clientMsgId": "m"})
	rep := <-done
	if rep.OK() || rep.Completed != 8 || !strings.Contains(strings.Join(rep.Errors, "\n"), `"topic"`) {
		t.Errorf("want a topic mismatch at step 9, got completed=%d errors=%v", rep.Completed, rep.Errors)
	}
}

func TestBadOptions(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	for _, opt := range []Options{
		{},
		{Conns: []string{"a"}, Order: "middle"},
		{Conns: []string{"a"}, Faults: []Fault{{Kind: "explode", Step: 1}}},
		{Conns: []string{"a"}, Faults: []Fault{{Kind: "drop", Step: 0}}},
		{Conns: []string{"a"}, Faults: []Fault{{Kind: "delay", Step: 99}}},
	} {
		if _, err := New(sc, tr, opt); err == nil {
			t.Errorf("options %+v accepted", opt)
		}
	}
}

func TestCorrelatorOneToOne(t *testing.T) {
	c := newCorrelator()
	if err := c.bind("clientMsgId", "a-1", "x"); err != nil {
		t.Fatal(err)
	}
	if err := c.bind("clientMsgId", "a-1", "y"); err == nil {
		t.Error("same recorded value bound to two client values")
	}
	if err := c.bind("clientMsgId", "a-2", "x"); err == nil {
		t.Error("one client message id stood for two recorded ones")
	}
	if err := c.bind("id", "s1", "1"); err != nil {
		t.Fatal(err)
	}
	if err := c.bind("id", "s2", "1"); err != nil {
		t.Errorf("request ids may be reused: %v", err)
	}
	f := map[string]any{"id": "s1", "clientMsgId": "a-1", "reqId": "untouched"}
	c.rewrite(f)
	if f["id"] != "1" || f["clientMsgId"] != "x" || f["reqId"] != "untouched" {
		t.Errorf("rewrite: %v", f)
	}
}

// No golden currently holds an optional frame (the documented race did not
// happen while recording), so inject one to cover both settings.
func TestOptionalFrames(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	extra := json.RawMessage(`{"action":"pres","topic":"${uid:B}","what":"on","online":true,"presRev":"<rev>"}`)
	tr.Steps[10].Optional = map[string][]json.RawMessage{"a": {extra}}
	for _, skip := range []bool{false, true} {
		opt := p2pOptions()
		opt.SkipOptional = skip
		r := runReplay(t, sc, tr, opt, faultSet{})
		r.check(t)
		want := 2
		if skip {
			want = 1
		}
		if len(r.order[11]) != want {
			t.Errorf("skip=%v: step 11 delivered %d frames, want %d", skip, len(r.order[11]), want)
		}
	}
}

// With AllowExtra an unscripted request is skipped without a reply and without
// disturbing the id correspondence; the scripted request still passes.
func TestAllowExtraSkipsUnscripted(t *testing.T) {
	sc, tr := loadOne(t, "core/p2p")
	opt := p2pOptions()
	opt.AllowExtra = true
	srv, err := New(sc, tr, opt)
	if err != nil {
		t.Fatal(err)
	}
	hs := httptest.NewServer(srv)
	defer hs.Close()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/"
	client := newScriptClient(t, url, srv, sc, tr, srv.opt)
	done := make(chan *Report, 1)
	go func() { done <- srv.Run() }()
	for i := range sc.Steps {
		idx := i + 1
		if idx == 9 {
			// A history pull the script does not have, using the id the
			// scripted pub is about to use.
			_ = client.send("a", map[string]any{"action": "get", "id": "cli-9", "topic": "x", "limit": 5})
		}
		if err := client.act(idx, sc.Steps[i]); err != nil {
			t.Fatal(err)
		}
		if err := client.receive(idx, sc.Steps[i], tr.Steps[i], faultSet{}, time.Now(), nil); err != nil {
			t.Fatalf("step %d: %v", idx, err)
		}
	}
	rep := <-done
	if !rep.OK() {
		t.Fatalf("replay failed: %v", rep.Errors)
	}
	if !strings.Contains(strings.Join(rep.Events, "\n"), "ignored unscripted frame on a") {
		t.Errorf("skip not reported: %v", rep.Events)
	}
}

func TestMatchRequestLeavesNoTraceOnFailure(t *testing.T) {
	c := newCorrelator()
	want := map[string]any{"action": "pub", "topic": "t", "clientMsgId": "a-1"}
	bad := map[string]any{"action": "pub", "topic": "WRONG", "clientMsgId": "x", "id": "1"}
	if err := matchRequest(want, bad, "s9", false, c); err == nil {
		t.Fatal("mismatch accepted")
	}
	good := map[string]any{"action": "pub", "topic": "t", "clientMsgId": "y", "id": "2"}
	if err := matchRequest(want, good, "s9", false, c); err != nil {
		t.Fatalf("rejected frame left a binding behind: %v", err)
	}
}
