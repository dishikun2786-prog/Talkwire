// Package replay plays the server's side of a golden transcript to a client
// under test, so a client SDK can be checked without a real server.
//
// The client under test plays one scenario user (its connections are the
// "subject" connections). For every step the replay waits for what the
// subject is scripted to do (connect, send a request, close), checks the
// request against the transcript, and sends back exactly the frames the
// reference server sent to the subject at that step. Everyone else in the
// scenario is simulated: their actions only show up as the frames the
// subject receives.
//
// Faults can be injected per step: delay, drop, duplicate and disconnect.
package replay

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"example.com/imkit/tools/conformance/harness"
)

// Fault is one injected misbehaviour.
//
//	delay       wait Ms before sending the step's frames (Step 0 = every step)
//	drop        do not send the step's frames
//	duplicate   send every frame of the step twice (Step 0 = every step)
//	disconnect  before the step, close Conn (empty = every open subject
//	            connection), then wait for the client to reconnect and log in
//	            again; the login is answered with a synthesized success
type Fault struct {
	Kind string `json:"kind"`
	Step int    `json:"step"`
	Conn string `json:"conn,omitempty"`
	Ms   int    `json:"ms,omitempty"`
}

// Options control one replay.
type Options struct {
	// Conns are the subject connections, as named in the scenario. Use
	// ConnsForUser to pick every connection of one scenario user.
	Conns []string
	// Order puts the reply to the subject's request "first" (default) or
	// "last" among the step's frames. Transcripts keep frames as a multiset,
	// so the reference server's order inside one step is not recorded.
	Order string
	// SkipOptional leaves out frames the reference server sends only some of
	// the time (documented races).
	SkipOptional bool
	// Timeout bounds every wait for the client (default 10s).
	Timeout time.Duration
	// TimeScale multiplies scripted waits ("wait" steps). 0 skips them.
	TimeScale float64
	// AllowExtra tolerates client frames the script does not expect: they are
	// logged and skipped (no reply), and the replay keeps waiting for the
	// scripted request.
	AllowExtra bool
	// Settle is how long to wait after the last step for frames the client
	// should not send (default 100ms).
	Settle time.Duration
	Faults []Fault
}

// Report is the outcome of a replay.
type Report struct {
	Scenario  string   `json:"scenario"`
	Conns     []string `json:"conns"`
	Steps     int      `json:"steps"`
	Completed int      `json:"completed"`
	Errors    []string `json:"errors,omitempty"`
	Events    []string `json:"events,omitempty"`
}

// OK reports whether the client followed the script.
func (r *Report) OK() bool { return len(r.Errors) == 0 && r.Completed == r.Steps }

// ConnsForUser returns the connections a scenario user logs in on, in order.
func ConnsForUser(sc *harness.Scenario, user string) []string {
	var out []string
	seen := map[string]bool{}
	for _, st := range sc.Steps {
		if st.Do == "login" && st.User == user && !seen[st.Conn] {
			seen[st.Conn] = true
			out = append(out, st.Conn)
		}
	}
	return out
}

// Server is one replay. It is an http.Handler for the WebSocket endpoint;
// Run drives the script.
type Server struct {
	sc       *harness.Scenario
	tr       *harness.Transcript
	opt      Options
	b        *Bindings
	corr     *Correlator
	subjects map[string]bool
	userOf   map[string]string
	// recovering marks connections re-established after an injected
	// disconnect, until their next scripted request.
	recovering map[string]bool

	upgrader websocket.Upgrader
	accepted chan *peer

	mu    sync.Mutex
	conns map[string]*peer
	rev   int64
	rep   *Report
}

type peer struct {
	ws     *websocket.Conn
	inbox  chan []byte
	done   chan struct{}
	wmu    sync.Mutex
	closed bool
}

// New prepares a replay of transcript tr (recorded from scenario sc).
func New(sc *harness.Scenario, tr *harness.Transcript, opt Options) (*Server, error) {
	if len(sc.Steps) != len(tr.Steps) {
		return nil, fmt.Errorf("scenario %s has %d steps, transcript has %d", sc.Name, len(sc.Steps), len(tr.Steps))
	}
	if len(opt.Conns) == 0 {
		return nil, errors.New("no subject connections")
	}
	if opt.Timeout == 0 {
		opt.Timeout = 10 * time.Second
	}
	if opt.Settle == 0 {
		opt.Settle = 100 * time.Millisecond
	}
	if opt.Order == "" {
		opt.Order = "first"
	}
	if opt.Order != "first" && opt.Order != "last" {
		return nil, fmt.Errorf("order must be first or last, not %q", opt.Order)
	}
	for _, f := range opt.Faults {
		switch f.Kind {
		case "delay", "drop", "duplicate", "disconnect":
		default:
			return nil, fmt.Errorf("unknown fault kind %q", f.Kind)
		}
		if f.Step < 0 || f.Step > len(sc.Steps) {
			return nil, fmt.Errorf("fault %s: step %d out of range 0..%d", f.Kind, f.Step, len(sc.Steps))
		}
		if f.Step == 0 && (f.Kind == "drop" || f.Kind == "disconnect") {
			return nil, fmt.Errorf("fault %s needs a step (only delay and duplicate accept 0 = every step)", f.Kind)
		}
	}
	s := &Server{
		sc: sc, tr: tr, opt: opt,
		b:          NewBindings(sc, tr),
		corr:       newCorrelator(),
		subjects:   map[string]bool{},
		userOf:     map[string]string{},
		recovering: map[string]bool{},
		upgrader:   websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }, ReadBufferSize: 4096, WriteBufferSize: 4096},
		accepted:   make(chan *peer, 16),
		conns:      map[string]*peer{},
		rev:        1,
		rep:        &Report{Scenario: sc.Name, Conns: opt.Conns, Steps: len(sc.Steps)},
	}
	for _, c := range opt.Conns {
		s.subjects[c] = true
	}
	for _, st := range sc.Steps {
		if st.Do == "login" {
			s.userOf[st.Conn] = st.User
		}
	}
	return s, nil
}

// Bindings exposes the concrete value of every placeholder (for test clients).
func (s *Server) Bindings() *Bindings { return s.b }

// ServeHTTP accepts a WebSocket connection from the client under test.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(1 << 20)
	p := &peer{ws: ws, inbox: make(chan []byte, 1024), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		for {
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			p.inbox <- data
		}
	}()
	s.accepted <- p
}

func (p *peer) send(data []byte) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	if p.closed {
		return errors.New("connection closed")
	}
	return p.ws.WriteMessage(websocket.TextMessage, data)
}

func (p *peer) close() {
	p.wmu.Lock()
	if !p.closed {
		p.closed = true
		_ = p.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	}
	p.wmu.Unlock()
	_ = p.ws.Close()
}

func (s *Server) errorf(step int, format string, a ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rep.Errors = append(s.rep.Errors, fmt.Sprintf("step %d: ", step)+fmt.Sprintf(format, a...))
}

func (s *Server) eventf(step int, format string, a ...any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rep.Events = append(s.rep.Events, fmt.Sprintf("step %d: ", step)+fmt.Sprintf(format, a...))
}

// Run plays the whole script and returns the report. It stops at the first
// error, since everything after a divergence would only add noise.
func (s *Server) Run() *Report {
	defer func() {
		for _, p := range s.conns {
			p.close()
		}
	}()
	for i := range s.sc.Steps {
		idx := i + 1
		if err := s.step(idx, s.sc.Steps[i], s.tr.Steps[i]); err != nil {
			s.errorf(idx, "%v", err)
			return s.rep
		}
		s.rep.Completed = idx
	}
	// Anything the client sent that the script never asked for. A short
	// settle lets late frames arrive before the check.
	time.Sleep(s.opt.Settle)
	for name, p := range s.conns {
		for {
			select {
			case data := <-p.inbox:
				s.unexpected(len(s.sc.Steps), name, data)
				continue
			default:
			}
			break
		}
	}
	return s.rep
}

func (s *Server) unexpected(step int, conn string, data []byte) {
	if s.opt.AllowExtra {
		s.eventf(step, "ignored unexpected frame on %s: %s", conn, clip(data))
		return
	}
	s.errorf(step, "unexpected frame on %s: %s", conn, clip(data))
}

func (s *Server) faults(kind string, idx int) []Fault {
	var out []Fault
	for _, f := range s.opt.Faults {
		if f.Kind == kind && (f.Step == idx || (f.Step == 0 && (kind == "delay" || kind == "duplicate"))) {
			out = append(out, f)
		}
	}
	return out
}

func (s *Server) step(idx int, st harness.Step, rec harness.StepRecord) error {
	for _, f := range s.faults("disconnect", idx) {
		if err := s.disconnect(idx, f.Conn); err != nil {
			return err
		}
	}
	subject := s.subjects[st.Conn]
	replyID := fmt.Sprintf("s%d", idx)
	var request map[string]any
	switch {
	case st.Do == "connect" && subject:
		p, err := s.accept()
		if err != nil {
			return fmt.Errorf("waiting for %s to connect: %w", st.Conn, err)
		}
		s.conns[st.Conn] = p
	case st.Do == "close" && subject:
		p := s.conns[st.Conn]
		if p == nil {
			return fmt.Errorf("%s is not connected", st.Conn)
		}
		if err := s.awaitClose(idx, st.Conn, p); err != nil {
			return err
		}
		delete(s.conns, st.Conn)
	case (st.Do == "login" || st.Do == "send" || st.Do == "raw") && subject:
		var err error
		replyID, request, err = s.request(idx, st, replyID)
		if err != nil {
			return err
		}
	case st.Do == "wait":
		if s.opt.TimeScale > 0 {
			time.Sleep(time.Duration(float64(st.Ms)*s.opt.TimeScale) * time.Millisecond)
		}
	}
	if err := s.emit(idx, st, rec, replyID, request); err != nil {
		return err
	}
	for _, name := range rec.Closed {
		if p := s.conns[name]; p != nil && s.subjects[name] {
			p.close()
			<-p.done
			delete(s.conns, name)
		}
	}
	return nil
}

// request reads the client's scripted request for a login, send or raw step
// and checks it against the scenario. It returns the recorded reply id and the
// decoded request.
//
// After an injected disconnect a client may resubscribe on its own. Until the
// next scripted request arrives on that connection, unscripted "sub" requests
// are answered the way the reference server answers them (code 200, the
// request's id and topic; the presence snapshot a p2p subscription also gets
// is not synthesized).
func (s *Server) request(idx int, st harness.Step, replyID string) (string, map[string]any, error) {
	for {
		data, err := s.next(idx, st.Conn)
		if err != nil {
			return replyID, nil, err
		}
		if st.Do == "raw" {
			return replyID, nil, nil // any frame will do; it exists to provoke a reaction
		}
		var got map[string]any
		if err := json.Unmarshal(data, &got); err != nil {
			return replyID, nil, fmt.Errorf("%s sent invalid JSON: %s", st.Conn, clip(data))
		}
		if st.Do == "login" {
			if got["action"] != "login" {
				return replyID, nil, fmt.Errorf("%s: expected a login, got %s", st.Conn, clip(data))
			}
			gs, _ := got["id"].(string)
			return replyID, got, s.corr.bind("id", replyID, gs)
		}
		var want map[string]any
		if err := json.Unmarshal([]byte(s.b.ExpandString(string(st.Msg))), &want); err != nil {
			return replyID, nil, fmt.Errorf("scenario message: %w", err)
		}
		if s.recovering[st.Conn] && got["action"] == "sub" && !(want["action"] == "sub" && want["topic"] == got["topic"]) {
			if err := s.answerResub(idx, st.Conn, got); err != nil {
				return replyID, nil, err
			}
			continue
		}
		_, strictID := want["id"]
		if strictID {
			replyID, _ = want["id"].(string)
		}
		if err := matchRequest(want, got, replyID, strictID, s.corr); err != nil {
			if s.opt.AllowExtra {
				// Skip it and keep waiting for the scripted request.
				s.eventf(idx, "ignored unscripted frame on %s (%v): %s", st.Conn, err, clip(data))
				continue
			}
			return replyID, nil, fmt.Errorf("%s sent a different request: %v (got %s)", st.Conn, err, clip(data))
		}
		s.recovering[st.Conn] = false
		return replyID, got, nil
	}
}

func (s *Server) answerResub(idx int, conn string, got map[string]any) error {
	reply := map[string]any{"action": "sub", "code": 200, "topic": got["topic"]}
	if id, ok := got["id"].(string); ok && id != "" {
		reply["id"] = id
	}
	out, _ := json.Marshal(reply)
	s.eventf(idx, "%s resubscribed to %v after the injected disconnect", conn, got["topic"])
	return s.conns[conn].send(out)
}

func (s *Server) accept() (*peer, error) {
	select {
	case p := <-s.accepted:
		return p, nil
	case <-time.After(s.opt.Timeout):
		return nil, errors.New("timed out")
	}
}

// next returns the next frame the client sends on conn. Frames on other
// subject connections are left queued for their own turn.
func (s *Server) next(idx int, conn string) ([]byte, error) {
	p := s.conns[conn]
	if p == nil {
		return nil, fmt.Errorf("%s is not connected", conn)
	}
	select {
	case data := <-p.inbox:
		return data, nil
	case <-p.done:
		select {
		case data := <-p.inbox:
			return data, nil
		default:
		}
		return nil, fmt.Errorf("%s closed the connection while a request was expected", conn)
	case <-time.After(s.opt.Timeout):
		return nil, fmt.Errorf("timed out waiting for %s to send a request", conn)
	}
}

func (s *Server) awaitClose(idx int, conn string, p *peer) error {
	deadline := time.After(s.opt.Timeout)
	for {
		select {
		case data := <-p.inbox:
			s.unexpected(idx, conn, data)
			if !s.opt.AllowExtra {
				return fmt.Errorf("%s sent a frame instead of closing", conn)
			}
		case <-p.done:
			p.wmu.Lock()
			p.closed = true
			p.wmu.Unlock()
			return nil
		case <-deadline:
			return fmt.Errorf("timed out waiting for %s to close", conn)
		}
	}
}

// disconnect closes subject connections, then waits for each to come back
// and log in, answering with a synthesized success (no id, as the reference
// server does).
func (s *Server) disconnect(idx int, only string) error {
	var names []string
	for name := range s.conns {
		if only == "" || name == only {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		s.conns[name].close()
		<-s.conns[name].done
		delete(s.conns, name)
		s.eventf(idx, "disconnected %s", name)
	}
	for _, name := range names {
		p, err := s.accept()
		if err != nil {
			return fmt.Errorf("after injected disconnect, waiting for %s to reconnect: %w", name, err)
		}
		s.conns[name] = p
		data, err := s.next(idx, name)
		if err != nil {
			return fmt.Errorf("after injected disconnect: %w", err)
		}
		var got map[string]any
		if json.Unmarshal(data, &got) != nil || got["action"] != "login" {
			return fmt.Errorf("after injected disconnect, %s must log in first, got %s", name, clip(data))
		}
		uid, _ := s.b.Value("uid:" + s.userOf[name])
		ack, _ := json.Marshal(map[string]any{"action": "login", "code": 200, "uid": uid})
		if err := p.send(ack); err != nil {
			return err
		}
		s.recovering[name] = true
		s.eventf(idx, "%s reconnected and logged in", name)
	}
	return nil
}

// emit sends the subject the frames the reference server sent at this step.
func (s *Server) emit(idx int, st harness.Step, rec harness.StepRecord, replyID string, request map[string]any) error {
	drop := len(s.faults("drop", idx)) > 0
	copies := 1 + len(s.faults("duplicate", idx))
	var delay time.Duration
	for _, f := range s.faults("delay", idx) {
		delay += time.Duration(f.Ms) * time.Millisecond
	}
	names := make([]string, 0, len(rec.Frames)+len(rec.Optional))
	seen := map[string]bool{}
	for _, g := range []map[string][]json.RawMessage{rec.Frames, rec.Optional} {
		for name := range g {
			if s.subjects[name] && !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	if len(names) > 0 && delay > 0 {
		s.eventf(idx, "delayed %s", delay)
		time.Sleep(delay)
	}
	for _, name := range names {
		frames := append([]json.RawMessage{}, rec.Frames[name]...)
		if !s.opt.SkipOptional {
			frames = append(frames, rec.Optional[name]...)
		}
		if name == st.Conn {
			frames = s.orderReply(frames, replyID, request)
		}
		if drop {
			s.eventf(idx, "dropped %d frame(s) for %s", len(frames), name)
			continue
		}
		p := s.conns[name]
		if p == nil {
			return fmt.Errorf("transcript has frames for %s but it is not connected", name)
		}
		for _, raw := range frames {
			out, err := s.render(raw)
			if err != nil {
				return err
			}
			for c := 0; c < copies; c++ {
				if err := p.send(out); err != nil {
					return fmt.Errorf("sending to %s: %w", name, err)
				}
			}
		}
		if copies > 1 {
			s.eventf(idx, "duplicated %d frame(s) for %s", len(frames), name)
		}
	}
	return nil
}

// orderReply moves the reply to the subject's own request to the front (or
// the back). A reply carries the request's id or reqId; a successful login or
// presence snapshot has neither and is recognised by its action.
func (s *Server) orderReply(frames []json.RawMessage, replyID string, request map[string]any) []json.RawMessage {
	if request == nil {
		return frames
	}
	action, _ := request["action"].(string)
	reqID, _ := request["reqId"].(string)
	var reply, rest []json.RawMessage
	for _, raw := range frames {
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		id, hasID := m["id"].(string)
		rq, _ := m["reqId"].(string)
		isReply := (hasID && id == replyID) || (reqID != "" && rq == s.recordedReqID(reqID)) ||
			(!hasID && m["action"] == action && m["code"] != nil)
		if isReply {
			reply = append(reply, raw)
		} else {
			rest = append(rest, raw)
		}
	}
	if s.opt.Order == "last" {
		return append(rest, reply...)
	}
	return append(reply, rest...)
}

func (s *Server) recordedReqID(actual string) string {
	if r, ok := s.corr.toRecord["reqId"][actual]; ok {
		return r
	}
	return actual
}

// render turns a recorded frame into what goes on the wire: placeholders get
// their values, <time> the current time, <rev> a rising counter, and the
// client's own correlation values replace the recorded ones.
func (s *Server) render(raw json.RawMessage) ([]byte, error) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	v = s.fill(s.b.Expand(v))
	if m, ok := v.(map[string]any); ok {
		s.corr.rewrite(m)
	}
	return json.Marshal(v)
}

func (s *Server) fill(v any) any {
	switch x := v.(type) {
	case string:
		switch x {
		case wildTime:
			return time.Now().UnixMilli()
		case wildRev:
			s.rev++
			return s.rev
		}
		return x
	case []any:
		for i := range x {
			x[i] = s.fill(x[i])
		}
		return x
	case map[string]any:
		for k := range x {
			x[k] = s.fill(x[k])
		}
		return x
	}
	return v
}

func clip(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:197] + "..."
	}
	return s
}
