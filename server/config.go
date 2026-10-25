package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"example.com/imkit/server/push"
)

type Config struct {
	Port                  int
	DBHost                string
	DBPort                int
	DBUser                string
	DBPassword            string
	DBName                string
	DBSSLMode             string
	JWTSecret             string
	FCMServiceAccountPath string
	MainAPIBaseURL        string
	// HostMode is how the host is reached: "http" (MainAPIBaseURL) or
	// "standalone" (no host process; see package standalone).
	HostMode string
	// StandaloneConfig is the standalone host's JSON file. Empty means
	// allow-all relations, no profiles and built-in configuration.
	StandaloneConfig string
	// Modules are the optional modules to install. Unless ModulesExplicit,
	// it is every module and those the host cannot support are skipped.
	Modules         []string
	ModulesExplicit bool
	// Push providers besides FCM (FCMServiceAccountPath). Each is enabled by
	// its key file; see package push.
	APNs    push.APNsConfig
	WebPush push.WebPushConfig
	// PushTimeout bounds each push request, including token exchanges.
	PushTimeout time.Duration
	// Profile is the protocol profile: ProfileLegacy (the default) or
	// ProfileStrict; see profile.go.
	Profile string
}

// LoadConfig builds the config from defaults plus environment overrides.
// Secrets (DB_PASSWORD, JWT_SECRET) deliberately have no defaults: they must be
// supplied by the environment, and startup aborts if either one is missing.
func LoadConfig() *Config {
	cfg := &Config{
		Port:           6062,
		DBHost:         "localhost",
		DBPort:         5432,
		DBUser:         "xiaotaozi",
		DBName:         "xiaotaozi",
		DBSSLMode:      "disable",
		MainAPIBaseURL: "http://127.0.0.1:6001",
		HostMode:       "http",
	}
	if v := os.Getenv("IM_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.Port = port
		}
	}
	if v := os.Getenv("DB_HOST"); v != "" {
		cfg.DBHost = v
	}
	if v := os.Getenv("DB_PORT"); v != "" {
		if port, err := strconv.Atoi(v); err == nil {
			cfg.DBPort = port
		}
	}
	if v := os.Getenv("DB_USER"); v != "" {
		cfg.DBUser = v
	}
	if v := os.Getenv("DB_PASSWORD"); v != "" {
		cfg.DBPassword = v
	}
	if v := os.Getenv("DB_NAME"); v != "" {
		cfg.DBName = v
	}
	if v := os.Getenv("DB_SSLMODE"); v != "" {
		cfg.DBSSLMode = v
	}
	if v := os.Getenv("JWT_SECRET"); v != "" {
		cfg.JWTSecret = v
	}
	// Both spellings are accepted on purpose. The deployed service sets
	// FCM_SERVICE_ACCOUNT_KEY_PATH, this file only ever read
	// FCM_SERVICE_ACCOUNT_PATH, and nothing reconciled the two — so the key was
	// never loaded and offline push had silently never worked. Reading both
	// means neither an existing deployment nor a fresh one can get this wrong.
	if v := os.Getenv("FCM_SERVICE_ACCOUNT_KEY_PATH"); v != "" {
		cfg.FCMServiceAccountPath = v
	} else if v := os.Getenv("FCM_SERVICE_ACCOUNT_PATH"); v != "" {
		cfg.FCMServiceAccountPath = v
	}
	if v := os.Getenv("MAIN_API_BASE_URL"); v != "" {
		cfg.MainAPIBaseURL = v
	}
	if v := os.Getenv("IMKIT_HOST_MODE"); v != "" {
		cfg.HostMode = v
	}
	cfg.StandaloneConfig = os.Getenv("IMKIT_STANDALONE_CONFIG")
	// IMKIT_MODULES: a comma-separated list, or "none". Unset installs every
	// module the host supports.
	cfg.Modules = moduleNames()
	if v, ok := os.LookupEnv("IMKIT_MODULES"); ok {
		cfg.ModulesExplicit = true
		cfg.Modules = nil
		if strings.TrimSpace(v) != "none" {
			for _, name := range strings.Split(v, ",") {
				if name = strings.TrimSpace(name); name != "" {
					cfg.Modules = append(cfg.Modules, name)
				}
			}
		}
	}
	loadPushConfig(cfg)
	cfg.Profile = ProfileLegacy
	if v := os.Getenv("IMKIT_PROTOCOL_PROFILE"); v != "" {
		cfg.Profile = v
	}
	if cfg.Profile != ProfileLegacy && cfg.Profile != ProfileStrict {
		log.Fatalf("IMKIT_PROTOCOL_PROFILE must be legacy or strict, got %q", cfg.Profile)
	}
	if cfg.HostMode != "http" && cfg.HostMode != "standalone" {
		log.Fatalf("IMKIT_HOST_MODE must be http or standalone, got %q", cfg.HostMode)
	}
	if cfg.StandaloneConfig != "" && cfg.HostMode != "standalone" {
		log.Fatal("IMKIT_STANDALONE_CONFIG is set but IMKIT_HOST_MODE is not standalone")
	}
	if cfg.DBPassword == "" {
		log.Fatal("DB_PASSWORD is required: set it in the service environment")
	}
	if cfg.JWTSecret == "" {
		log.Fatal("JWT_SECRET is required: it must match the main server's JWT_SECRET")
	}
	return cfg
}

// loadPushConfig reads the APNs and Web Push settings.
//
//	IMKIT_APNS_KEY_PATH, IMKIT_APNS_KEY_ID, IMKIT_APNS_TEAM_ID, IMKIT_APNS_TOPIC
//	IMKIT_APNS_ENV                production (default) or sandbox
//	IMKIT_VAPID_KEY_PATH, IMKIT_VAPID_SUBJECT
//	IMKIT_WEBPUSH_ALLOWED_HOSTS   push services subscriptions may name
//	IMKIT_WEBPUSH_TTL_SEC         how long a push service keeps a message
//	IMKIT_PUSH_HTTP_TIMEOUT_SEC   per request; IM_FCM_HTTP_TIMEOUT_SEC is the
//	                              older name, still read
func loadPushConfig(cfg *Config) {
	cfg.PushTimeout = envDuration("IMKIT_PUSH_HTTP_TIMEOUT_SEC", envDuration("IM_FCM_HTTP_TIMEOUT_SEC", 10*time.Second))
	cfg.APNs = push.APNsConfig{
		KeyPath: os.Getenv("IMKIT_APNS_KEY_PATH"),
		KeyID:   os.Getenv("IMKIT_APNS_KEY_ID"),
		TeamID:  os.Getenv("IMKIT_APNS_TEAM_ID"),
		Topic:   os.Getenv("IMKIT_APNS_TOPIC"),
		BaseURL: push.APNsProduction,
		Timeout: cfg.PushTimeout,
	}
	switch v := os.Getenv("IMKIT_APNS_ENV"); v {
	case "", "production":
	case "sandbox":
		cfg.APNs.BaseURL = push.APNsSandbox
	default:
		log.Fatalf("IMKIT_APNS_ENV must be production or sandbox, got %q", v)
	}
	cfg.WebPush = push.WebPushConfig{
		KeyPath: os.Getenv("IMKIT_VAPID_KEY_PATH"),
		Subject: os.Getenv("IMKIT_VAPID_SUBJECT"),
		Hosts:   push.DefaultWebPushHosts,
		TTL:     envDuration("IMKIT_WEBPUSH_TTL_SEC", 24*time.Hour),
		Timeout: cfg.PushTimeout,
	}
	if v, ok := os.LookupEnv("IMKIT_WEBPUSH_ALLOWED_HOSTS"); ok {
		cfg.WebPush.Hosts = push.ParseHosts(v)
	}
}

// envInt reads a positive integer from an env var, falling back to def when
// unset, unparseable or non-positive. Same rationale as envDuration: a typo
// in an optional tuning knob must not take the service down.
func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Printf("ignoring invalid %s=%q, using %d", name, v, def)
		return def
	}
	return n
}

// Channel buffer sizes for the hub's register/unregister and presence
// queues. These were inline literals in NewHub; they are the two places a
// large deployment most often needs headroom (a reconnect storm after a
// network blip fills register/unregister; a big group coming online at once
// fills presenceCh), and both are cheap memory-wise, so they are worth an
// operator-facing knob rather than a rebuild.
var (
	hubChanBuf      = envInt("IM_HUB_CHAN_BUF", 100)
	presenceChanBuf = envInt("IM_PRESENCE_CHAN_BUF", 1024)
)

// RuntimeConfig holds the group-chat limits that the main Node server owns
// (category "group_chat" in its config registry) and this service pulls over
// HTTP.
//
// 为什么这些值不放在 im-go 自己的常量里：它们同时约束三端——服务端校验、
// Node 侧的管理后台展示、Android 侧的输入框/分享面板上限。写死在 Go 常量里
// 意味着管理员改一个"群成员上限"要重新编译并重启 IM 服务，而端上那份还对
// 不上。真相源放在 Node 的配置注册表，im-go 与 App 各自拉取，改一处三端一致。
type RuntimeConfig struct {
	MaxMembers          int  `json:"maxMembers"`
	MaxAdmins           int  `json:"maxAdmins"`
	NameMaxLen          int  `json:"nameMaxLen"`
	MessageMaxLen       int  `json:"messageMaxLen"`
	ForwardBundleMax    int  `json:"forwardBundleMax"`
	ForwardBroadcastMax int  `json:"forwardBroadcastMax"`
	HistoryPageSize     int  `json:"historyPageSize"`
	FCMEnabled          bool `json:"fcmEnabled"`
	FCMConcurrency      int  `json:"fcmConcurrency"`
	// ReadReceiptEnabled gates group read-cursor persistence (handleNote) and
	// the read-cursor snapshot in handleGet. This key already existed on the
	// Node side as group_chat.read_receipt_enabled -- reused here rather than
	// adding a second switch for the same feature. Same "not repaired" note
	// as FCMEnabled: false is a legitimate operator choice, not a sentinel
	// for "field missing" in Go's zero-value JSON decoding.
	ReadReceiptEnabled bool `json:"readReceiptEnabled"`
	// QrTtlSeconds is how long a freshly minted group_qr_token stays
	// resolvable before ResolveGroupQrToken starts answering "not found" --
	// see MintGroupQrToken. Same three-tier truth source as every other field
	// here: Node config registry -> this fetched snapshot -> the default
	// below if unreachable.
	QrTtlSeconds int `json:"qrTtlSeconds"`
	// PublishRateLimitPer10s bounds how many handlePub calls a single
	// authenticated uid may make in a rolling-ish 10-second window (see
	// tryAcquirePublish in hub.go). This is the only per-message throttle in
	// the entire publish path -- handlePub otherwise only checks length, not
	// rate, so a runaway or malicious client could otherwise flood a topic
	// as fast as the network allows. Applies uniformly to P2P and group
	// topics; per-uid rather than per-topic so opening more topics can't
	// multiply one client's effective rate.
	PublishRateLimitPer10s int `json:"publishRateLimitPer10s"`

	// CallRingTimeoutSeconds/CallMaxDurationMinutes back the IM voice/video
	// calling feature's im_call.ring_timeout_seconds / im_call.max_duration_minutes
	// config keys (IM_VOICE_VIDEO_CALL_PLAN.md §4.3/§5). They ride on this
	// same struct/refresh cycle -- not a separate one the way E2eeRuntimeConfig
	// is -- purely because the plan explicitly calls for reusing "whatever
	// struct/refresh path already carries group_chat.* runtime config into
	// this file" rather than standing up a fourth HTTP round trip for two
	// integers; they are not otherwise related to group chat.
	//
	// CallRingTimeoutSeconds is read exactly once per call, at the moment
	// handleCallInvite starts the per-session time.AfterFunc -- an admin
	// changing it mid-ring does not retroactively shorten/lengthen a call
	// that is already ringing, only the next one to start ringing after the
	// next refresh tick. CallMaxDurationMinutes is not currently read by any
	// im-go code path (the authoritative enforcement lives in roubao's own
	// periodic sweep per plan §5's last bullet -- "同 VoiceCallHub.sweepStale()
	// 的设计哲学"); it is fetched here anyway per the plan's explicit
	// instruction, in case a future im-go-side enforcement path is added
	// without needing a second config round trip to be wired up first.
	CallRingTimeoutSeconds int `json:"callRingTimeoutSeconds"`
	CallMaxDurationMinutes int `json:"callMaxDurationMinutes"`
}

// defaultRuntimeConfig is what the service runs on before the first
// successful fetch, and what it keeps running on if the main server is
// unreachable. The values match the Node registry's own defaults, so a
// deployment that never manages to fetch behaves exactly like one that
// fetched an untouched config -- rather than, say, falling back to zeros and
// silently rejecting every message for being longer than 0 characters.
func defaultRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{
		MaxMembers:          500,
		MaxAdmins:           10,
		NameMaxLen:          30,
		MessageMaxLen:       20000,
		ForwardBundleMax:    50,
		ForwardBroadcastMax: 20,
		HistoryPageSize:     50,
		FCMEnabled:          true,
		FCMConcurrency:      16,
		ReadReceiptEnabled:  false,
		// 7 days, matching WeChat's own group QR code lifetime.
		QrTtlSeconds: 604800,
		// Matches the Node registry's own default for
		// group_chat.publish_rate_limit_per_10s.
		PublishRateLimitPer10s: 20,
		// Match the Node registry's own defaults for im_call.ring_timeout_seconds
		// / im_call.max_duration_minutes (IM_VOICE_VIDEO_CALL_PLAN.md §5).
		CallRingTimeoutSeconds: 45,
		CallMaxDurationMinutes: 60,
	}
}

// runtimeConfig is read on the hot path (every pub, every forward) and
// written only by the refresh goroutine, so it is an atomic value holding an
// immutable snapshot rather than a mutex-guarded struct: readers never
// block, and a reader can never observe a half-applied config where, say,
// MaxMembers is new but MaxAdmins is still old.
var runtimeConfig atomic.Value // holds RuntimeConfig

func init() {
	runtimeConfig.Store(defaultRuntimeConfig())
}

// CurrentRuntimeConfig returns the live snapshot. It always returns a usable
// config -- never a zero struct -- because runtimeConfig is seeded with the
// defaults at init and a failed refresh leaves the previous value in place.
// Callers must read it through this function on every use instead of caching
// it, or an admin's change never reaches them.
func CurrentRuntimeConfig() RuntimeConfig {
	if v, ok := runtimeConfig.Load().(RuntimeConfig); ok {
		return v
	}
	return defaultRuntimeConfig()
}

// normalizeHistoryLimit is the single normalization point for a client-supplied
// history page size.
//
// It exists because that policy used to be written twice, differently:
// hub.handleGet fell back to CurrentRuntimeConfig().HistoryPageSize above a
// ceiling of 200, while store.GetMessages and store.GetMessagesAfter each
// independently hardcoded `limit <= 0 || limit > 100 -> limit = 50`. The store
// ran last, so a request for 101..200 was waved through by the hub and then
// silently squashed to 50, and raising group_chat.history_page_size past 100
// changed nothing at all -- the configured value was overwritten by a literal
// one call deeper. Route every caller through here; a ceiling re-typed inline
// somewhere else reproduces that bug exactly and will not fail to compile.
//
// Out-of-range falls back to the configured default rather than clamping to
// the ceiling: a client asking for 100000 rows has miscalculated something,
// and 200 rows is no more what it meant than 50 was.
func normalizeHistoryLimit(limit int) int {
	// Matches the Node config registry's own 20..200 bound on
	// group_chat.history_page_size, so a value an operator is allowed to set
	// can never be rejected here as out of range.
	const maxHistoryPageSize = 200
	if limit <= 0 || limit > maxHistoryPageSize {
		return CurrentRuntimeConfig().HistoryPageSize
	}
	return limit
}

// setRuntimeConfig installs a freshly fetched snapshot, repairing any field
// the main server sent as zero/negative with the default. A single bad field
// in the response must not be able to disable a limit entirely (MaxMembers=0
// would reject every invite) or divide-by-zero a concurrency limiter.
func setRuntimeConfig(rc RuntimeConfig) {
	def := defaultRuntimeConfig()
	fix := func(v, d int) int {
		if v <= 0 {
			return d
		}
		return v
	}
	rc.MaxMembers = fix(rc.MaxMembers, def.MaxMembers)
	rc.MaxAdmins = fix(rc.MaxAdmins, def.MaxAdmins)
	rc.NameMaxLen = fix(rc.NameMaxLen, def.NameMaxLen)
	rc.MessageMaxLen = fix(rc.MessageMaxLen, def.MessageMaxLen)
	rc.ForwardBundleMax = fix(rc.ForwardBundleMax, def.ForwardBundleMax)
	rc.ForwardBroadcastMax = fix(rc.ForwardBroadcastMax, def.ForwardBroadcastMax)
	rc.HistoryPageSize = fix(rc.HistoryPageSize, def.HistoryPageSize)
	rc.FCMConcurrency = fix(rc.FCMConcurrency, def.FCMConcurrency)
	rc.QrTtlSeconds = fix(rc.QrTtlSeconds, def.QrTtlSeconds)
	rc.PublishRateLimitPer10s = fix(rc.PublishRateLimitPer10s, def.PublishRateLimitPer10s)
	rc.CallRingTimeoutSeconds = fix(rc.CallRingTimeoutSeconds, def.CallRingTimeoutSeconds)
	rc.CallMaxDurationMinutes = fix(rc.CallMaxDurationMinutes, def.CallMaxDurationMinutes)
	// FCMEnabled and ReadReceiptEnabled are deliberately not repaired: false
	// is a legitimate value an operator sets on purpose, and there is no way
	// to tell it apart from "field missing" in Go's zero-value JSON decoding.
	runtimeConfig.Store(rc)
}

// E2eeRuntimeConfig holds the E2EE feature flags/limits the main Node server
// owns (category "e2ee"). A separate struct + separate fetch/refresh cycle
// from RuntimeConfig on purpose: that type and its doc comments are
// explicitly scoped to group_chat.* — folding an unrelated subsystem (crypto
// key material policy, not chat limits) into it would mean rewriting every
// comment there to stop claiming a scope it no longer has.
type E2eeRuntimeConfig struct {
	// Enabled is the global E2EE kill switch. False rejects
	// e2e_publish_bundle, e2e_send, and e2e_set_group_encrypted, and makes
	// e2e_fetch_bundle answer found:false (the same shape it already uses for
	// "target never published a bundle" — no new wire case needed). It does
	// NOT block e2e_terminate (opting out must never itself be blockable —
	// same reasoning handleE2eTerminate's own doc comment already gives for
	// skipping the friendship recheck) and does NOT withhold delivery of
	// already-queued offline envelopes on login: those messages were sent
	// before the switch flipped, and holding them back would silently drop
	// mail that is already rightfully the recipient's.
	Enabled bool `json:"enabled"`
	// GroupEnabled gates only the ability to newly turn ON group encryption
	// (handleE2eSetGroupEncrypted). A group already encrypted before this
	// flips to false keeps sending/receiving — same monotonic promise
	// ActionE2eSetGroupEncrypted's doc comment already makes to end users
	// ("no un-encrypting a group"), just enforced here against an operator
	// trying to stop new adoption too.
	GroupEnabled bool `json:"groupEnabled"`
	// EnvelopeTTLHours bounds how long an offline recipient's encrypted
	// payload sits in im_e2e_envelopes (see relayE2eEnvelope) before
	// StartE2eEnvelopeReaper deletes it unread. Replaces what used to be the
	// hardcoded e2eEnvelopeTTL constant (14 days).
	EnvelopeTTLHours int `json:"envelopeTtlHours"`
}

func defaultE2eeRuntimeConfig() E2eeRuntimeConfig {
	return E2eeRuntimeConfig{Enabled: true, GroupEnabled: true, EnvelopeTTLHours: 336}
}

// e2eeRuntimeConfig mirrors runtimeConfig's atomic-snapshot shape — see that
// var's doc comment for why (hot-path readers never block, no half-applied
// config is ever observable).
var e2eeRuntimeConfig atomic.Value // holds E2eeRuntimeConfig

func init() {
	e2eeRuntimeConfig.Store(defaultE2eeRuntimeConfig())
}

// CurrentE2eeConfig returns the live snapshot, always usable — see
// CurrentRuntimeConfig's doc comment for the same guarantee.
func CurrentE2eeConfig() E2eeRuntimeConfig {
	if v, ok := e2eeRuntimeConfig.Load().(E2eeRuntimeConfig); ok {
		return v
	}
	return defaultE2eeRuntimeConfig()
}

// e2eeEnvelopeTTL is EnvelopeTTLHours as a time.Duration, for the one call
// site (relayE2eEnvelope) that needs it in that shape.
func (c E2eeRuntimeConfig) e2eeEnvelopeTTL() time.Duration {
	return time.Duration(c.EnvelopeTTLHours) * time.Hour
}

// setE2eeRuntimeConfig installs a freshly fetched snapshot — see
// setRuntimeConfig's doc comment for why a non-positive numeric field is
// repaired with the default while the booleans are not (false is a
// legitimate operator choice, indistinguishable from "field missing" in Go's
// zero-value JSON decoding).
func setE2eeRuntimeConfig(rc E2eeRuntimeConfig) {
	if rc.EnvelopeTTLHours <= 0 {
		rc.EnvelopeTTLHours = defaultE2eeRuntimeConfig().EnvelopeTTLHours
	}
	e2eeRuntimeConfig.Store(rc)
}

// maxConnections caps how many WebSocket connections ServeWS will accept at
// once, checked and reserved (via tryAcquireConnSlot) before the upgrade
// itself. Auth in this protocol happens in-band via a login message sent
// after the WS handshake completes (see hub.go ProcessMessage), not at
// upgrade time -- so without an admission cap, every accepted upgrade spawns
// two goroutines (ReadPump/WritePump, see handler.go) for a socket that has
// not proven it belongs to anyone yet, and CheckOrigin unconditionally
// returns true (see handler.go). A burst of connection attempts -- a
// reconnect storm after a server restart, or a deliberate flood -- could
// otherwise grow unboundedly. The default is generous relative to this
// deployment's realistic concurrent user count, so it should never be hit by
// legitimate traffic; it exists purely as a burst ceiling.
var maxConnections = envInt("IM_MAX_CONNECTIONS", 10000)

// connCount is the live count of accepted-and-not-yet-disconnected WS
// connections. Package-level atomic rather than a Hub field so ServeWS
// (handler.go) can check it before a Client -- and therefore a Hub
// registration -- exists at all.
var connCount atomic.Int64

// tryAcquireConnSlot reserves one connection slot, reporting whether the cap
// allows it. Must be paired with exactly one later releaseConnSlot call --
// callers that fail after a successful acquire (e.g. the WS upgrade itself
// erroring out) must release immediately since no Client will exist to route
// the release through Disconnect.
func tryAcquireConnSlot() bool {
	if int(connCount.Add(1)) > maxConnections {
		connCount.Add(-1)
		return false
	}
	return true
}

// releaseConnSlot gives back one slot acquired by tryAcquireConnSlot.
func releaseConnSlot() {
	connCount.Add(-1)
}

func (c *Config) DSN() string {
	return "host=" + c.DBHost +
		" port=" + strconv.Itoa(c.DBPort) +
		" user=" + c.DBUser +
		" password=" + c.DBPassword +
		" dbname=" + c.DBName +
		" sslmode=" + c.DBSSLMode
}
