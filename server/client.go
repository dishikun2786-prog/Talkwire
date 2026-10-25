package main

import (
	"encoding/json"
	"log"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Connection timing/limits. writeWait and pongWait are overridable from the
// environment (see envDuration) because the right values depend on the
// network a deployment actually runs on -- cross-border clients on mobile
// networks need far more slack than a LAN -- and re-cutting a binary to
// change a timeout is not a reasonable operational loop.
var (
	writeWait = envDuration("IM_WRITE_WAIT_SEC", 10*time.Second)
	pongWait  = envDuration("IM_PONG_WAIT_SEC", 60*time.Second)
	// pingPeriod was pongWait*9/10, i.e. one ping every 54s against a 60s
	// read deadline: a single dropped ping (or a 6s network stall, which is
	// nothing on a mobile network) meant the deadline expired before the
	// next ping could refresh it, and the connection dropped for no real
	// reason. At 0.45 there are always two pings inside one pongWait
	// window, so it takes two consecutive losses to kill the connection --
	// the usual rule for any keepalive with a deadline behind it.
	pingPeriod = pingPeriodFor(pongWait)
	// maxMessageSize was 10MB "for file sharing", but no file bytes have
	// ever crossed this socket: media is uploaded over HTTP and referenced
	// here by fileUrl, so a frame is only text plus small metadata. The 10MB
	// limit therefore protected nothing and let a single hostile frame make
	// the server allocate 10MB per connection.
	//
	// The headroom argument used to cite a compile-time constant
	// (maxMessageContentLen = 20k runes) that is now gone -- content length is
	// enforced against CurrentRuntimeConfig().MessageMaxLen instead. The
	// conclusion is unchanged because the bound is now an operator-facing one:
	// the Node config registry caps group_chat.message_max_len at 20000 runes
	// (default 5000), and 20000 runes is at most ~78KB of UTF-8, so even a
	// maximally-raised ceiling plus the JSON envelope stays well inside 256KB.
	// If that registry maximum is ever raised, re-check this number -- a frame
	// limit below the content limit rejects legitimate messages at the socket,
	// before any handler can produce a useful error.
	maxMessageSize int64 = 256 * 1024
)

// envDuration reads a whole number of seconds from an env var, falling back
// to def when unset, unparseable, or non-positive. Silently ignoring bad
// input rather than aborting startup is deliberate for tuning knobs: a typo
// in an optional timeout override should not take the IM service down.
func envDuration(name string, def time.Duration) time.Duration {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("[Client] ignoring invalid %s=%q, using %s", name, v, def)
		return def
	}
	return time.Duration(n) * time.Second
}

// Client represents a single WebSocket connection
type Client struct {
	hub        *Hub
	conn       *websocket.Conn
	send       chan []byte
	uid        string // IM user ID (uid_xxx)
	appUserID  string // original app user UUID
	device     string // the device from login; "" when the login named none
	loginTime  time.Time
	mu         sync.Mutex
	subscribed map[string]bool // topics this client is subscribed to
	closed     bool
	// gen orders the logins of one hub; a connection that replaced another
	// has the higher generation. Set once at login.
	gen atomic.Uint64
}

func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	return &Client{
		hub:        hub,
		conn:       conn,
		send:       make(chan []byte, 256),
		subscribed: make(map[string]bool),
	}
}

// readPump reads messages from the WebSocket connection
func (c *Client) ReadPump() {
	defer func() {
		c.Disconnect()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("[Client] Read error: %v", err)
			}
			break
		}

		c.handleMessage(message)
	}
}

// WritePump writes messages from the hub to the WebSocket connection
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Disconnect()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// SendJSON sends a JSON message to the client
func (c *Client) SendJSON(msg *ServerMessage) {
	c.SendAny(msg)
}

// SendAny marshals and sends any JSON-serializable value. Unlike SendJSON,
// which is typed to the flat do-everything ServerMessage struct, this is used
// for the batch-forward/group-AI-QA response types (ForwardBundleResult,
// GroupAiSettingsMessage, ...) that use a "type" discriminator and their own
// purpose-built field set instead.
func (c *Client) SendAny(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("[Client] Marshal error: %v", err)
		return
	}
	c.enqueue(data)
}

// SendRaw sends an already-serialized frame. It exists so a message being
// fanned out to many recipients can be marshalled once by the caller instead
// of once per recipient: BroadcastToTopic used to hand the same
// *ServerMessage to every subscriber's SendJSON, so an N-member group paid N
// identical json.Marshal calls (and N allocations of the same bytes) for one
// message.
//
// The caller must not mutate data afterwards -- every recipient's send
// channel now holds the same backing array.
func (c *Client) SendRaw(data []byte) {
	c.enqueue(data)
}

// TrySendJSON is SendJSON's non-destructive sibling: it reports whether the
// frame actually made it into the send channel instead of assuming so and
// force-closing the connection on a full buffer (see enqueue's doc comment
// for why that's the right call for ordinary pushes). It exists for exactly
// one caller class: relayE2eEnvelope, where a full buffer should fall back
// to the offline queue (im_e2e_envelopes) rather than tear down the
// connection — an E2EE envelope, unlike a regular message, has no seq/cursor
// for pullOfflineMessages to ever notice was missing after a forced
// reconnect, so the existing "close to force resync" recovery this
// server relies on everywhere else silently does NOT recover it.
func (c *Client) TrySendJSON(msg *ServerMessage) bool {
	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("[Client] Marshal error: %v", err)
		return false
	}
	select {
	case c.send <- data:
		return true
	default:
		return false
	}
}

// enqueue is the single place a frame enters a client's send channel, so the
// buffer-full policy below cannot drift between SendAny and SendRaw.
func (c *Client) enqueue(data []byte) {
	select {
	case c.send <- data:
	default:
		// Silently dropping here left a permanent hole in the recipient's seq
		// series: nothing marks the topic dirty, the client has no per-topic
		// gap detection, and its only recovery (pullOfflineMessages) asks for
		// `seq > MAX(local seq)` — so any gap *below* the local maximum is
		// never requested again by any code path. Closing the connection
		// instead forces a reconnect, which re-runs the offline pull and
		// downgrades permanent loss to one round of latency.
		//
		// Off-goroutine because Disconnect blocking-sends on hub.unregister,
		// and this runs on arbitrary goroutines including the hub's own.
		log.Printf("[Client] Send buffer full for %s, closing connection to force resync", c.UID())
		go c.Disconnect()
	}
}

// handleMessage processes an incoming message from the client
func (c *Client) handleMessage(raw []byte) {
	var msg ClientMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		// 帧本身没解出来，拿不到请求 id，只能回空串。客户端对 action="error" 且
		// 没有同名在飞请求的响应会直接丢弃，不会误配到别的请求上。
		c.SendJSON(ErrorResponse("", "error", CodeBadReq, "invalid JSON: "+err.Error()))
		return
	}

	if msg.Action == "" {
		return
	}

	// Delegate to hub for processing
	c.hub.ProcessMessage(c, &msg)
}

// UID returns the client's IM user id under c.mu.
//
// F-39: uid is written by the login handler on the read goroutine and read
// from arbitrary other goroutines (the hub's fan-out, the presence worker,
// SendAny's buffer-full log, Disconnect's own guard). Every other mutable
// field on Client is already covered by c.mu; uid was the one that was not,
// making those reads a genuine data race — not a benign one, since a
// concurrent read can observe a half-written string header and produce
// garbage or a panic.
//
// The uid field itself stays package-visible because hub.go reads it in many
// places; those reads are being migrated to this accessor rather than
// changed all at once. Inside client.go, always use UID()/setUID().
func (c *Client) UID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.uid
}

// setUID records the authenticated identity. Called once, from the login
// handler, before the client is registered with the hub.
func (c *Client) setUID(uid string) {
	c.mu.Lock()
	c.uid = uid
	c.mu.Unlock()
}

// AppUserID returns the client's original app-user UUID under c.mu.
//
// Same class of bug as F-39 above, newly introduced by the IM voice/video
// call feature: handleLogin wrote client.appUserID as a bare field
// assignment, and resolveAppUserID reads a *different* client's appUserID
// (via GetClientByUID, from whatever goroutine is currently handling a
// call_invite for some other connection) as a bare field read. That is a
// genuine cross-goroutine data race on a string header, not a benign one —
// it currently "works" only because login happens to complete, in practice,
// before any other client goes looking for this uid, not because anything
// enforces that ordering. Mirrors UID()/setUID() exactly; new code should
// use these instead of touching c.appUserID directly.
func (c *Client) AppUserID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.appUserID
}

// setAppUserID records the resolved app-user UUID. Called once, from the
// login handler, before the client is registered with the hub — same
// lifecycle as setUID.
func (c *Client) setAppUserID(appUserID string) {
	c.mu.Lock()
	c.appUserID = appUserID
	c.mu.Unlock()
}

// Device returns the device the client logged in from, under c.mu.
func (c *Client) Device() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.device
}

// setDevice records the device. Called once, from the login handler, before
// the client is registered with the hub.
func (c *Client) setDevice(device string) {
	c.mu.Lock()
	c.device = device
	c.mu.Unlock()
}

// key is the client's slot in the connection table and the topic index.
func (c *Client) key() connKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	return connKey{c.uid, c.device}
}

// IsAuthenticated returns true if the client has logged in
func (c *Client) IsAuthenticated() bool {
	return c.UID() != ""
}

// Subscribe adds a topic to the client's subscription set and — for real
// broadcastable topics — registers this client in the hub's topic index so
// BroadcastToTopic can reach it without scanning every connected client.
// "me"/"fnd" are per-client pseudo-topics that are never broadcast to, so
// they're kept out of the shared index.
func (c *Client) Subscribe(topic string) {
	c.mu.Lock()
	c.subscribed[topic] = true
	c.mu.Unlock()
	if topic != TopicMe && topic != TopicFnd {
		c.hub.subscribeTopic(topic, c)
	}
}

// Unsubscribe removes a topic from the client's subscription set and the
// hub's topic index.
func (c *Client) Unsubscribe(topic string) {
	c.mu.Lock()
	delete(c.subscribed, topic)
	c.mu.Unlock()
	if topic != TopicMe && topic != TopicFnd {
		c.hub.unsubscribeTopic(topic, c)
	}
}

// IsSubscribed checks if the client is subscribed to a topic
func (c *Client) IsSubscribed(topic string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subscribed[topic]
}

// drainSubscriptions atomically empties the client's subscription set and
// returns the topics it held, so the hub can clean up its topic index on
// disconnect. Uses only its own lock, never the hub's, so callers never
// hold both locks at once.
func (c *Client) drainSubscriptions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	topics := make([]string, 0, len(c.subscribed))
	for t := range c.subscribed {
		topics = append(topics, t)
	}
	c.subscribed = make(map[string]bool)
	return topics
}

// Disconnect cleans up the client connection
func (c *Client) Disconnect() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()

	// Give back the admission slot ServeWS reserved for this connection
	// (see tryAcquireConnSlot in config.go). Safe to do unconditionally here
	// because the c.closed guard above already makes this whole function
	// run at most once per Client, regardless of which of ReadPump/WritePump
	// triggers it first.
	releaseConnSlot()

	// Notify hub. Read through the accessor rather than touching c.uid
	// directly: this runs on the read/write pump goroutines while the login
	// handler may still be assigning uid on another one (F-39).
	if c.UID() != "" {
		c.hub.unregister <- c
	}

	c.conn.Close()
}

// IsOnline reports whether this connection has not been torn down yet.
// Reads c.closed under c.mu for the same reason UID() does — Disconnect can
// run on either pump goroutine (F-39).
func (c *Client) IsOnline() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return !c.closed
}

func logf(format string, args ...interface{}) {
	log.Printf("[IM] "+format, args...)
}

// pingPeriodFor is the keepalive interval for a read deadline: 0.45 of it
// (see pingPeriod).
func pingPeriodFor(readDeadline time.Duration) time.Duration {
	return time.Duration(float64(readDeadline) * 0.45)
}
