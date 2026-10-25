package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"example.com/imkit/server/host/hmacjwt"
	"example.com/imkit/server/host/standalone"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

// The concurrency tests run the real hub, store and WebSocket handler
// in-process and talk to them over loopback WebSockets, the way a client
// does. Like the other database tests they need IMKIT_TEST_DSN.

const liveSecret = "imkit-live-test-secret"

// frame is one decoded server frame.
type frame map[string]any

func (f frame) str(k string) string {
	s, _ := f[k].(string)
	return s
}

func (f frame) num(k string) int64 {
	n, _ := f[k].(float64)
	return int64(n)
}

type liveServer struct {
	t     *testing.T
	hub   *Hub
	store *Store
	srv   *httptest.Server

	mu    sync.Mutex
	conns []*liveConn
}

type liveOptions struct {
	// connCap, when set, caps the server at this many connections more than
	// were open when it started.
	connCap int
}

func newLiveServer(t *testing.T, opts liveOptions) *liveServer {
	t.Helper()
	store := testStore(t)
	hostImpl, err := standalone.New(standalone.Options{})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHub(store, hmacjwt.New(liveSecret), nil, hostImpl)
	if _, err := installModules(h, hostImpl, nil, true); err != nil {
		t.Fatal(err)
	}
	go h.Run()
	go h.runPresenceWorker()

	base := connCount.Load()
	prevCap := maxConnections
	if opts.connCap > 0 {
		// Set before the server starts: its handler goroutines read it.
		maxConnections = int(base) + opts.connCap
	}
	ls := &liveServer{t: t, hub: h, store: store}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", NewWebSocketHandler(h).ServeWS)
	mux.HandleFunc("/ws-stalled", ls.serveStalled)
	ls.srv = httptest.NewServer(mux)
	t.Cleanup(func() {
		ls.mu.Lock()
		conns := ls.conns
		ls.mu.Unlock()
		for _, c := range conns {
			c.close()
		}
		ls.srv.Close()
		maxConnections = prevCap
		// Let the server side of every connection unwind, so the next test
		// starts from the same connection count.
		deadline := time.Now().Add(5 * time.Second)
		for connCount.Load() > base && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if n := connCount.Load() - base; n > 0 {
			t.Errorf("%d connection slots still taken after the test", n)
		}
		for len(h.presenceCh) > 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	})
	return ls
}

// serveStalled accepts a connection whose frames are never written: a
// client that stopped reading, without waiting for socket buffers to fill.
func (ls *liveServer) serveStalled(w http.ResponseWriter, r *http.Request) {
	if !tryAcquireConnSlot() {
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		releaseConnSlot()
		return
	}
	go NewClient(ls.hub, conn).ReadPump()
}

// accessToken is an access token for appUser.
func accessToken(appUser string) string {
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": appUser, "type": "access", "role": "app_user",
		"exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(liveSecret))
	if err != nil {
		panic(err)
	}
	return s
}

// liveConn is the client end of one connection. Every frame it receives is
// kept; waitFor takes frames off a queue in arrival order.
type liveConn struct {
	t   *testing.T
	ws  *websocket.Conn
	uid string

	wmu sync.Mutex // gorilla allows one writer at a time

	mu    sync.Mutex
	queue []frame
	seen  []frame
	wake  chan struct{}
	done  chan struct{}
	ids   atomic.Int64
}

func (ls *liveServer) dial(path string) (*liveConn, *http.Response, error) {
	url := "ws" + strings.TrimPrefix(ls.srv.URL, "http") + path
	ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, resp, err
	}
	c := &liveConn{t: ls.t, ws: ws, wake: make(chan struct{}, 1), done: make(chan struct{})}
	ls.mu.Lock()
	ls.conns = append(ls.conns, c)
	ls.mu.Unlock()
	go c.readLoop()
	return c, resp, nil
}

// connect dials and logs appUser in.
func (ls *liveServer) connect(appUser string) *liveConn {
	return ls.connectAt("/ws", appUser)
}

func (ls *liveServer) connectAt(path, appUser string) *liveConn {
	ls.t.Helper()
	c, _, err := ls.dial(path)
	if err != nil {
		ls.t.Fatalf("dial %s: %v", path, err)
	}
	if path == "/ws-stalled" {
		// No reply can arrive: wait until the hub has the login instead.
		c.send(frame{"action": ActionLogin, "token": accessToken(appUser)})
		return c
	}
	reply := c.login(appUser)
	if reply.num("code") != CodeOK {
		ls.t.Fatalf("login %s: %v", appUser, reply)
	}
	return c
}

// login sends a login and returns its reply (which carries no id).
func (c *liveConn) login(appUser string) frame {
	c.t.Helper()
	c.send(frame{"action": ActionLogin, "token": accessToken(appUser)})
	f, ok := c.waitFor(func(f frame) bool { return f.str("action") == ActionLogin }, 5*time.Second)
	if !ok {
		c.t.Fatalf("no login reply for %s", appUser)
	}
	c.uid = f.str("uid")
	return f
}

func (c *liveConn) readLoop() {
	defer close(c.done)
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		var f frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		c.mu.Lock()
		c.queue = append(c.queue, f)
		c.seen = append(c.seen, f)
		c.mu.Unlock()
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// send writes a frame. A write to a connection the server closed is not an
// error here: tests that race connections against each other expect it.
func (c *liveConn) send(f frame) {
	data, err := json.Marshal(f)
	if err != nil {
		panic(err)
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
	c.ws.WriteMessage(websocket.TextMessage, data)
}

// request sends f with a fresh id and waits for the reply carrying it.
func (c *liveConn) request(f frame) frame {
	c.t.Helper()
	id := fmt.Sprintf("q%d", c.ids.Add(1))
	f["id"] = id
	c.send(f)
	reply, ok := c.waitFor(func(r frame) bool { return r.str("id") == id }, 5*time.Second)
	if !ok {
		c.t.Fatalf("no reply to %v", f)
	}
	return reply
}

// waitFor takes the first queued frame matching pred, waiting up to d.
func (c *liveConn) waitFor(pred func(frame) bool, d time.Duration) (frame, bool) {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		c.mu.Lock()
		for i, f := range c.queue {
			if pred(f) {
				c.queue = append(c.queue[:i:i], c.queue[i+1:]...)
				c.mu.Unlock()
				return f, true
			}
		}
		c.mu.Unlock()
		select {
		case <-c.wake:
		case <-c.done:
			// Frames read before the close may still match.
			c.mu.Lock()
			for i, f := range c.queue {
				if pred(f) {
					c.queue = append(c.queue[:i:i], c.queue[i+1:]...)
					c.mu.Unlock()
					return f, true
				}
			}
			c.mu.Unlock()
			return nil, false
		case <-deadline.C:
			return nil, false
		}
	}
}

// frames returns every frame received so far that matches pred.
func (c *liveConn) frames(pred func(frame) bool) []frame {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []frame
	for _, f := range c.seen {
		if pred(f) {
			out = append(out, f)
		}
	}
	return out
}

func (c *liveConn) closed() bool {
	select {
	case <-c.done:
		return true
	default:
		return false
	}
}

func (c *liveConn) waitClosed(d time.Duration) bool {
	select {
	case <-c.done:
		return true
	case <-time.After(d):
		return false
	}
}

func (c *liveConn) close() {
	c.ws.Close()
	<-c.done
}

// presOf matches presence pushes (not snapshots) about uid.
func presOf(uid string) func(frame) bool {
	return func(f frame) bool {
		return f.str("action") == ActionPres && f.str("topic") == uid && (f.str("what") == PresOnline || f.str("what") == PresOffline)
	}
}

// dataWith matches a data frame carrying clientMsgId or content.
func dataWith(key, value string) func(frame) bool {
	return func(f frame) bool { return f.str("action") == "data" && f.str(key) == value }
}
