// Command loadtest drives an Talkwire server with many users at once (plan 9.3):
// they connect and log in, form chat pairs and groups, then send messages at
// a steady rate for a while. It measures how long logging in, the pub ack and
// delivery to the other members take, and counts what was lost.
//
//	LOADTEST_JWT_SECRET=<the server's JWT_SECRET> go run . -url ws://127.0.0.1:6062/ws/im -users 500
//
// Run the server with a host that lets everyone chat (the standalone host's
// default) and a publish limit above -rate (see README.md). Each run uses
// fresh user ids, so runs do not see each other's history.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
)

type options struct {
	url       string
	users     int
	groupSize int
	rate      float64
	duration  time.Duration
	ramp      time.Duration
	drain     time.Duration
	timeout   time.Duration
	prefix    string
	jsonOut   string
	maxLoss   float64
	strict    bool
}

func main() {
	var o options
	flag.StringVar(&o.url, "url", "ws://127.0.0.1:6062/ws/im", "the server's WebSocket endpoint")
	flag.IntVar(&o.users, "users", 100, "users, one connection each")
	flag.IntVar(&o.groupSize, "group-size", 10, "members per group (0: no groups); users are also paired for direct chats")
	flag.Float64Var(&o.rate, "rate", 1, "messages per second per user, alternating between the partner and the group")
	flag.DurationVar(&o.duration, "duration", 30*time.Second, "how long to send")
	flag.DurationVar(&o.ramp, "ramp", 10*time.Second, "spread the connections over this long")
	flag.DurationVar(&o.drain, "drain", 10*time.Second, "wait at most this long after sending for the last deliveries")
	flag.DurationVar(&o.timeout, "timeout", 15*time.Second, "for connecting, logging in and creating a group")
	flag.StringVar(&o.prefix, "prefix", "", "user id prefix (default: a fresh one per run)")
	flag.StringVar(&o.jsonOut, "json", "", "also write the report as JSON to this file")
	flag.Float64Var(&o.maxLoss, "max-loss", 0, "fail (exit 1) when more than this fraction of deliveries is lost")
	flag.BoolVar(&o.strict, "strict-token", true, "sign strict-profile tokens (type=access, role=app_user)")
	flag.Parse()

	secret := os.Getenv("LOADTEST_JWT_SECRET")
	if len(secret) < 32 {
		fmt.Fprintln(os.Stderr, "set LOADTEST_JWT_SECRET to the server's JWT_SECRET (at least 32 characters)")
		os.Exit(2)
	}
	if o.prefix == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		o.prefix = "lt" + hex.EncodeToString(b) + "-"
	}
	if o.users < 2 || o.rate <= 0 {
		fmt.Fprintln(os.Stderr, "need -users >= 2 and -rate > 0")
		os.Exit(2)
	}

	r := run(o, []byte(secret))
	r.print(os.Stdout)
	if o.jsonOut != "" {
		data, _ := json.MarshalIndent(r, "", "  ")
		if err := os.WriteFile(o.jsonOut, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "write report:", err)
		}
	}
	if !r.passed(o.maxLoss) {
		os.Exit(1)
	}
}

// uid is the user's wire uid (protocol section 5.1).
func uid(appUserID string) string {
	sum := sha256.Sum256([]byte(appUserID))
	return "uid_" + base64.RawURLEncoding.EncodeToString(sum[:8])
}

func token(secret []byte, userID string, strict bool) (string, error) {
	claims := jwt.MapClaims{"sub": userID, "exp": time.Now().Add(2 * time.Hour).Unix()}
	if strict {
		claims["type"] = "access"
		claims["role"] = "app_user"
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

// ------------------------------------------------------------------ client

type sent struct {
	at     time.Time
	expect int // deliveries this message should make
}

type client struct {
	userID, uid string
	conn        *websocket.Conn
	wmu         sync.Mutex
	nextID      atomic.Int64

	pending sync.Map // request id -> sent
	replies sync.Map // request id -> chan reply (login, create_grp)

	partner *client
	group   string
	members int
	stats   *stats
}

// frame is what the client sends.
type frame map[string]any

// reply is what the client reads of a server frame: only the fields it
// needs, so decoding stays cheap when thousands of frames arrive a second.
type reply struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Code    int    `json:"code"`
	Topic   string `json:"topic"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

func (c *client) send(f frame) error {
	data, _ := json.Marshal(f)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return c.conn.WriteMessage(websocket.TextMessage, data)
}

// call sends a request and waits for the reply with the same id.
func (c *client) call(f frame, timeout time.Duration) (reply, error) {
	id, _ := f["id"].(string)
	ch := make(chan reply, 1)
	c.replies.Store(id, ch)
	defer c.replies.Delete(id)
	if err := c.send(f); err != nil {
		return reply{}, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-time.After(timeout):
		return reply{}, errors.New("no reply")
	}
}

func (c *client) read(closed *atomic.Bool) {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			if !closed.Load() {
				c.stats.count(&c.stats.UnexpectedClose)
			}
			return
		}
		now := time.Now()
		var f reply
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		action := f.Action
		// The login reply has no id under legacy (protocol 6.1).
		if action == "login" {
			if ch, ok := c.replies.Load("login"); ok {
				ch.(chan reply) <- f
			}
			continue
		}
		if id := f.ID; id != "" {
			if ch, ok := c.replies.Load(id); ok {
				ch.(chan reply) <- f
				continue
			}
			if v, ok := c.pending.LoadAndDelete(id); ok {
				s := v.(sent)
				if f.Code == 200 {
					c.stats.ack(now.Sub(s.at))
				} else {
					c.stats.ackFailed(f.Code, s.expect)
				}
				continue
			}
		}
		if action == "data" {
			if at, ok := sentAt(f.Content); ok {
				c.stats.delivered(strings.HasPrefix(f.Topic, "grp_"), now.Sub(at))
			}
		}
	}
}

// Message bodies carry their send time: "lt|<unix nanoseconds>|<filler>".
func body(at time.Time) string {
	return "lt|" + strconv.FormatInt(at.UnixNano(), 10) + "|hello from the load test"
}

func sentAt(content string) (time.Time, bool) {
	parts := strings.SplitN(content, "|", 3)
	if len(parts) < 2 || parts[0] != "lt" {
		return time.Time{}, false
	}
	n, err := strconv.ParseInt(parts[1], 10, 64)
	return time.Unix(0, n), err == nil
}

// ------------------------------------------------------------------ run

func run(o options, secret []byte) *report {
	st := newStats()
	var closed atomic.Bool
	clients := make([]*client, o.users)
	var wg sync.WaitGroup
	start := time.Now()
	gap := o.ramp / time.Duration(o.users)

	// 1. Connect and log in, spread over the ramp.
	for i := range clients {
		c := &client{userID: fmt.Sprintf("%s%05d", o.prefix, i), stats: st}
		c.uid = uid(c.userID)
		clients[i] = c
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := time.Now()
			dialer := websocket.Dialer{HandshakeTimeout: o.timeout}
			conn, _, err := dialer.Dial(o.url, nil)
			if err != nil {
				st.count(&st.ConnectFailed)
				st.note("connect: " + err.Error())
				return
			}
			c.conn = conn
			go c.read(&closed)
			tok, err := token(secret, c.userID, o.strict)
			if err != nil {
				st.count(&st.LoginFailed)
				return
			}
			r, err := c.call(frame{"id": "login", "action": "login", "token": tok}, o.timeout)
			if err != nil || r.Code != 200 {
				st.count(&st.LoginFailed)
				if err == nil {
					st.note(fmt.Sprintf("login: %d %s", r.Code, r.Reason))
				}
				_ = conn.Close()
				c.conn = nil
				return
			}
			st.login(time.Since(t0))
		}()
		time.Sleep(gap)
	}
	wg.Wait()
	st.ConnectSeconds = time.Since(start).Seconds()

	var online []*client
	for _, c := range clients {
		if c.conn != nil {
			online = append(online, c)
		}
	}
	st.Online = len(online)

	// 2. Pairs for direct chats; groups of groupSize.
	for i := 0; i+1 < len(online); i += 2 {
		online[i].partner, online[i+1].partner = online[i+1], online[i]
	}
	if o.groupSize >= 2 {
		for i := 0; i+o.groupSize <= len(online); i += o.groupSize {
			members := online[i : i+o.groupSize]
			uids := make([]string, 0, len(members)-1)
			for _, m := range members[1:] {
				uids = append(uids, m.uid)
			}
			owner := members[0]
			r, err := owner.call(frame{"id": fmt.Sprintf("g%d", i), "action": "create_grp", "name": fmt.Sprintf("load %d", i/o.groupSize), "members": uids}, o.timeout)
			if err != nil || r.Code != 201 {
				st.count(&st.GroupsFailed)
				if err == nil {
					st.note(fmt.Sprintf("create_grp: %d %s", r.Code, r.Reason))
				}
				continue
			}
			st.Groups++
			for _, m := range members {
				m.group, m.members = r.Topic, len(members)
			}
		}
		time.Sleep(time.Second) // the members' grp_sync, which subscribes them
	}

	// 3. Send at a steady rate, alternating between the partner and the group.
	stop := make(chan struct{})
	interval := time.Duration(float64(time.Second) / o.rate)
	sendStart := time.Now()
	for i, c := range online {
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(interval * time.Duration(i) / time.Duration(len(online))) // spread the first sends
			tick := time.NewTicker(interval)
			defer tick.Stop()
			toGroup := i%2 == 0
			for {
				select {
				case <-stop:
					return
				case <-tick.C:
				}
				topic, expect := "", 0
				switch {
				case toGroup && c.group != "":
					topic, expect = c.group, c.members-1
				case c.partner != nil:
					topic, expect = c.partner.uid, 1
				case c.group != "":
					topic, expect = c.group, c.members-1
				}
				toGroup = !toGroup
				if topic == "" {
					continue
				}
				now := time.Now()
				id := "p" + strconv.FormatInt(c.nextID.Add(1), 10)
				c.pending.Store(id, sent{at: now, expect: expect})
				st.sent(expect)
				if err := c.send(frame{"id": id, "action": "pub", "topic": topic, "content": body(now), "clientMsgId": c.userID + "-" + id}); err != nil {
					c.pending.Delete(id)
					st.writeFailed(expect)
				}
			}
		}()
	}
	time.Sleep(o.duration)
	close(stop)
	wg.Wait()
	st.SendSeconds = time.Since(sendStart).Seconds()

	// 4. Wait for the last acks and deliveries.
	deadline := time.Now().Add(o.drain)
	for time.Now().Before(deadline) && !st.settled() {
		time.Sleep(100 * time.Millisecond)
	}
	closed.Store(true)
	for _, c := range online {
		_ = c.conn.Close()
	}
	return st.report(o)
}
