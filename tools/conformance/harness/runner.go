package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dishikun2786-prog/Talkwire/tools/conformance/fakehost"
	"github.com/dishikun2786-prog/Talkwire/tools/conformance/token"
)

const (
	defaultSettle = 250 * time.Millisecond
	replyTimeout  = 4 * time.Second
)

// Env is what a scenario runs against.
type Env struct {
	WSURL    string
	HTTPBase string
	Secret   string
	Host     fakehost.Control
	Source   string // label stored in the transcript, e.g. "oracle 33e23250"
}

type wsConn struct {
	ws     *websocket.Conn
	mu     sync.Mutex
	buf    []json.RawMessage
	closed bool
}

func (c *wsConn) read() {
	for {
		_, data, err := c.ws.ReadMessage()
		c.mu.Lock()
		if err != nil {
			c.closed = true
			c.mu.Unlock()
			return
		}
		c.buf = append(c.buf, json.RawMessage(data))
		c.mu.Unlock()
	}
}

func (c *wsConn) find(pred func(map[string]any) bool) map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, raw := range c.buf {
		var m map[string]any
		if json.Unmarshal(raw, &m) == nil && pred(m) {
			return m
		}
	}
	return nil
}

func (c *wsConn) take() ([]json.RawMessage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.buf
	c.buf = nil
	return out, c.closed
}

// runState holds one scenario run.
type runState struct {
	sc     *Scenario
	env    Env
	conns  map[string]*wsConn
	closed map[string]bool
	vars   map[string]string
}

// Run executes a scenario and returns its raw (not yet normalized) transcript.
func Run(sc *Scenario, env Env) (*Transcript, error) {
	st := &runState{sc: sc, env: env, conns: map[string]*wsConn{}, closed: map[string]bool{}, vars: map[string]string{}}
	defer func() {
		for _, c := range st.conns {
			_ = c.ws.Close()
		}
	}()
	if _, err := env.Host.TakeCalls(); err != nil {
		return nil, err
	}
	settle := defaultSettle
	if sc.SettleMs > 0 {
		settle = time.Duration(sc.SettleMs) * time.Millisecond
	}
	tr := &Transcript{Scenario: sc.Name, Source: env.Source}
	for i, step := range sc.Steps {
		rec := StepRecord{Index: i + 1, Do: step.Do, Conn: step.Conn, Note: step.Note}
		if len(step.Msg) > 0 {
			rec.Sent = step.Msg
		}
		if err := st.exec(i+1, step, &rec); err != nil {
			return nil, fmt.Errorf("scenario %s step %d (%s): %w", sc.Name, i+1, step.Do, err)
		}
		time.Sleep(settle)
		rec.raw = map[string][]json.RawMessage{}
		names := make([]string, 0, len(st.conns))
		for name := range st.conns {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			frames, closed := st.conns[name].take()
			if len(frames) > 0 {
				rec.raw[name] = frames
			}
			if closed && !st.closed[name] {
				st.closed[name] = true
				rec.Closed = append(rec.Closed, name)
			}
		}
		calls, err := env.Host.TakeCalls()
		if err != nil {
			return nil, fmt.Errorf("scenario %s step %d: %w", sc.Name, i+1, err)
		}
		rec.hostCalls = append(rec.hostCalls, calls...)
		tr.Steps = append(tr.Steps, rec)
	}
	tr.vars = st.vars
	return tr, nil
}

var padPattern = regexp.MustCompile(`\$\{pad:(\d+)\}`)

func (st *runState) expand(s string) string {
	s = padPattern.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.Atoi(padPattern.FindStringSubmatch(m)[1])
		return strings.Repeat("x", n)
	})
	for _, u := range st.sc.Users {
		s = strings.ReplaceAll(s, "${user:"+u+"}", AppUserID(u))
		s = strings.ReplaceAll(s, "${uid:"+u+"}", IMUID(AppUserID(u)))
		for _, v := range st.sc.Users {
			s = strings.ReplaceAll(s, "${p2p:"+u+","+v+"}", P2PTopic(IMUID(AppUserID(u)), IMUID(AppUserID(v))))
		}
	}
	for k, v := range st.vars {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}

func (st *runState) conn(name string) (*wsConn, error) {
	c, ok := st.conns[name]
	if !ok {
		return nil, fmt.Errorf("connection %q is not open", name)
	}
	return c, nil
}

func (st *runState) exec(idx int, step Step, rec *StepRecord) error {
	switch step.Do {
	case "connect":
		ws, _, err := websocket.DefaultDialer.Dial(st.env.WSURL, nil)
		if err != nil {
			return err
		}
		c := &wsConn{ws: ws}
		go c.read()
		st.conns[step.Conn] = c
		st.closed[step.Conn] = false
		return nil
	case "close":
		c, err := st.conn(step.Conn)
		if err != nil {
			return err
		}
		_ = c.ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		_ = c.ws.Close()
		delete(st.conns, step.Conn)
		return nil
	case "wait":
		time.Sleep(time.Duration(step.Ms) * time.Millisecond)
		return nil
	case "friends":
		if len(step.Friends) != 3 {
			return fmt.Errorf("friends step needs [A, B, on|off]")
		}
		return st.env.Host.SetFriends(AppUserID(step.Friends[0]), AppUserID(step.Friends[1]), step.Friends[2] == "on")
	case "raw":
		c, err := st.conn(step.Conn)
		if err != nil {
			return err
		}
		// Write errors are tolerated here: a raw step is how a scenario sends
		// an oversized frame, and gorilla writes large messages as 4 KB
		// fragments, so the server's close (read limit) races the tail of
		// the write. Whether the connection closed is recorded in Closed,
		// which is deterministic; the write error is not.
		_ = c.ws.WriteMessage(websocket.TextMessage, []byte(st.expand(step.Raw)))
		return nil
	case "login":
		tok := step.Token
		if tok == "" {
			claims := map[string]any{}
			for k, v := range step.Claims {
				claims[k] = v
			}
			if claims["exp"] == "past" {
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			}
			var err error
			tok, err = token.User(st.env.Secret, AppUserID(step.User), time.Now(), claims)
			if err != nil {
				return err
			}
		}
		msg := map[string]any{"id": fmt.Sprintf("s%d", idx), "action": "login", "token": tok}
		return st.sendAndAwait(step, msg, rec)
	case "send":
		var msg map[string]any
		if err := json.Unmarshal([]byte(st.expand(string(step.Msg))), &msg); err != nil {
			return fmt.Errorf("msg: %w", err)
		}
		if _, has := msg["id"]; !has {
			msg["id"] = fmt.Sprintf("s%d", idx)
		}
		return st.sendAndAwait(step, msg, rec)
	case "host":
		return st.hostCall(step, rec)
	}
	return fmt.Errorf("unknown step %q", step.Do)
}

func (st *runState) sendAndAwait(step Step, msg map[string]any, rec *StepRecord) error {
	c, err := st.conn(step.Conn)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(msg)
	if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}
	if step.Await == "none" {
		return nil
	}
	id, _ := msg["id"].(string)
	action, _ := msg["action"].(string)
	match := func(m map[string]any) bool {
		if len(step.AwaitMatch) > 0 {
			for k, v := range step.AwaitMatch {
				if fmt.Sprint(m[k]) != st.expand(fmt.Sprint(v)) {
					return false
				}
			}
			return true
		}
		if got, ok := m["id"].(string); ok {
			return got == id
		}
		// Some reference-server replies (successful login, presence snapshot)
		// carry no id; fall back to the action for those only.
		return m["action"] == action
	}
	deadline := time.Now().Add(replyTimeout)
	for time.Now().Before(deadline) {
		if reply := c.find(match); reply != nil {
			for name, path := range step.Capture {
				v := lookup(reply, path)
				if v == nil {
					return fmt.Errorf("capture %s: no field %q in reply %v", name, path, reply)
				}
				st.vars[name] = fmt.Sprint(v)
			}
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec.TimedOut = true
	return nil
}

func lookup(m map[string]any, path string) any {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[part]
	}
	if f, ok := cur.(float64); ok && f == float64(int64(f)) {
		return int64(f)
	}
	return cur
}

func (st *runState) hostCall(step Step, rec *StepRecord) error {
	path := step.Call
	if !strings.HasPrefix(path, "/") {
		path = "/api/v1/internal/im/" + path
	}
	tok, err := token.Internal(st.env.Secret, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, st.env.HTTPBase+path, bytes.NewReader([]byte(st.expand(string(step.Body)))))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	out := map[string]any{"status": resp.StatusCode}
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		out["body"] = parsed
	} else {
		out["body"] = strings.TrimSpace(string(body))
	}
	rec.HostResponse = out
	return nil
}
