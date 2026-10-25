package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"example.com/imkit/server/host"
	"example.com/imkit/server/host/hmacjwt"
	"example.com/imkit/server/host/httpadapter"
	"example.com/imkit/server/host/jwks"
	"example.com/imkit/server/host/standalone"
	"example.com/imkit/server/push"
)

// runtimeConfigRefreshInterval is how often the group-chat limits are
// re-pulled from the main server. 60s is a deliberate compromise: an admin
// changing a limit in the console expects it to take effect "soon" without
// restarting anything, and the request itself is one small authenticated GET
// against a service already on the same host.
const runtimeConfigRefreshInterval = 60 * time.Second

func main() {
	log.SetPrefix("[IM] ")
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	log.Println("Starting IM Server...")

	// Load configuration
	cfg := LoadConfig()
	log.Printf("Config: port=%d, db=%s:%d/%s", cfg.Port, cfg.DBHost, cfg.DBPort, cfg.DBName)

	// Initialize database store
	store, err := NewStore(cfg.DSN())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer store.Close()

	auth, err := newIdentity(cfg)
	if err != nil {
		log.Fatalf("Identity setup failed: %v", err)
	}

	// The protocol profile is fixed before anything can send a frame.
	strictProtocol = cfg.Profile == ProfileStrict
	log.Printf("Protocol profile: %s", cfg.Profile)

	// Offline push through each configured provider.
	pusher, vapidKey := newPusher(cfg)

	// The host: over HTTP (friendship checks, profiles, media, config and the
	// modules' calls against the main server), or in-process.
	hostImpl, err := newHost(cfg)
	if err != nil {
		log.Fatalf("Host setup failed: %v", err)
	}

	// Initialize hub and the optional modules. Modules are installed before
	// the schema is migrated: only an installed module's tables are created.
	hub := NewHub(store, auth, pusher, hostImpl)
	hub.webPushHosts = cfg.WebPush.Hosts
	hub.vapidKey = vapidKey
	mods, err := installModules(hub, hostImpl, cfg.Modules, cfg.ModulesExplicit)
	if err != nil {
		log.Fatalf("Module setup failed: %v", err)
	}
	log.Printf("Modules: %v", mods.names())

	// Initialize tables
	moduleSets, err := mods.migrationSets()
	if err != nil {
		log.Fatalf("Failed to load module migrations: %v", err)
	}
	if err := store.InitTables(moduleSets...); err != nil {
		log.Fatalf("Failed to initialize tables: %v", err)
	}
	if !mods.has("e2ee") {
		warnEncryptedGroupsWithoutE2ee(store)
	}

	go hub.Run()
	// Presence transitions are fanned out by a single serializing worker, not
	// by one goroutine per event — see hub.runPresenceWorker for why the
	// unordered version made a reconnecting user look permanently offline.
	go hub.runPresenceWorker()
	// Periodic eviction for the forward_broadcast reqId idempotency cache
	// (see hub.go finishForwardBroadcast) — otherwise a long-running
	// process accumulates one map entry per forward request forever.
	go hub.gcForwardReqCache()
	// Reaps unacked im_pending_clears rows older than 30 days -- see
	// Store.StartPendingClearsReaper's doc comment for why that's safe.
	go store.StartPendingClearsReaper()
	// Same for the group membership/role event queue.
	go store.StartPendingGroupEventsReaper()
	// Keeps the group-chat limits (member cap, message length, forward
	// batch sizes, ...) in sync with the main server's config registry.
	// Started after the hub is already running: the service is fully
	// functional on the built-in defaults, so a slow or unreachable main
	// server must not hold up accepting connections.
	go StartRuntimeConfigRefresh(hostImpl, runtimeConfigRefreshInterval)
	// Module background work (the E2EE envelope reaper and its config refresh).
	mods.start(hub, hostImpl)

	// Initialize WebSocket handler
	wsHandler := NewWebSocketHandler(hub)

	// Setup HTTP routes
	mux := http.NewServeMux()

	// WebSocket endpoint
	mux.HandleFunc("/ws", wsHandler.ServeWS)
	mux.HandleFunc("/ws/im", wsHandler.ServeWS)

	// REST API endpoints
	mux.HandleFunc("/api/health", wsHandler.HealthCheck)
	mux.HandleFunc("/api/v1/im/user/register", wsHandler.RegisterUser)
	mux.HandleFunc("/api/v1/im/user/topics", wsHandler.GetTopics)
	// Local/internal admin bridge. These paths are not added to Caddy; shared
	// internal-service JWT verification is defense in depth for direct access.
	mux.HandleFunc("/api/v1/internal/im/contact-sync", wsHandler.InternalContactSync)
	mux.HandleFunc("/api/v1/internal/im/media-ref-count", wsHandler.InternalMediaRefCount)
	mux.HandleFunc("/api/v1/internal/im/topic-members-resolve", wsHandler.InternalTopicMembersResolve)
	mux.HandleFunc("/api/v1/internal/im/custom-broadcast", wsHandler.InternalCustomBroadcast)
	mux.HandleFunc("/api/v1/internal/im/red-packet-broadcast", wsHandler.InternalRedPacketBroadcast)
	// IM voice/video calling (IM_VOICE_VIDEO_CALL_PLAN.md §4.4) -- roubao's
	// per-call billing tick relays credit_balance/call_ended pushes through
	// here. No new background goroutine is needed for this feature: the
	// authoritative stuck-call sweep lives on roubao's side (plan §5's last
	// bullet), and every per-call timeout in this file is its own
	// time.AfterFunc rather than a periodic scan (see hub.go's callState).
	mux.HandleFunc("/api/v1/internal/im/call-broadcast", wsHandler.InternalCallBroadcast)
	mux.HandleFunc("/api/v1/internal/admin/im/topics", wsHandler.InternalAdminTopics)
	mux.HandleFunc("/api/v1/internal/admin/im/topic", wsHandler.InternalAdminTopic)
	mux.HandleFunc("/api/v1/internal/admin/im/messages", wsHandler.InternalAdminMessages)
	mux.HandleFunc("/api/v1/internal/admin/im/clear", wsHandler.InternalAdminClearTopic)

	// Start server
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Port)
	log.Printf("IM Server listening on %s", addr)

	// ReadHeaderTimeout/IdleTimeout were both unset, i.e. unlimited: a client
	// could open a connection, send one byte of a request line and hold a
	// goroutine plus a socket indefinitely (Slowloris). Deliberately no
	// ReadTimeout/WriteTimeout — those apply to the whole request, and this
	// mux also serves the WebSocket upgrade, whose "request" lasts as long as
	// the chat session does. The per-connection deadlines that do bound a live
	// socket are pongWait/writeWait in client.go.
	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// hostWithConfig is a host the server can run against: everything the hub
// asks of it plus the configuration documents main keeps applied.
type hostWithConfig interface {
	hostAPI
	host.Config
}

// newHost builds the host adapter the configuration asks for.
func newHost(cfg *Config) (hostWithConfig, error) {
	if cfg.HostMode == "standalone" {
		if cfg.StandaloneConfig == "" {
			log.Println("Host: standalone, allow-all relations, built-in configuration")
			return standalone.New(standalone.Options{})
		}
		log.Printf("Host: standalone, from %s", cfg.StandaloneConfig)
		return standalone.Load(cfg.StandaloneConfig)
	}
	log.Printf("Host: %s", cfg.MainAPIBaseURL)
	return httpadapter.New(cfg.MainAPIBaseURL, cfg.JWTSecret), nil
}

// / newIdentity builds the verifier of user and internal tokens. Internal
// tokens are always signed with the secret shared with the host; user tokens
// too, unless the host publishes a key set (IMKIT_JWKS_URL).
func newIdentity(cfg *Config) (host.Identity, error) {
	policy := hmacjwt.Policy{UserRoles: cfg.TokenUserRoles}
	if cfg.Profile == ProfileStrict {
		if cfg.AcceptLegacyTokens {
			log.Println("WARNING: IMKIT_ACCEPT_LEGACY_TOKENS=1: refresh, administrator and other non-access tokens can log in (red line R18); use it only while migrating clients")
		} else {
			policy.AccessOnly = true
		}
	}
	secret := hmacjwt.New(cfg.JWTSecret)
	if cfg.JWKSURL == "" {
		log.Printf("Identity: shared secret (HS256), access tokens only: %v", policy.AccessOnly)
		return secret.WithPolicy(policy), nil
	}
	v, err := jwks.New(cfg.JWKSURL, policy, secret, 3*time.Second)
	if err != nil {
		return nil, err
	}
	// Read now so a wrong URL shows at startup; logins retry on their own.
	if err := v.Refresh(); err != nil {
		logf("Identity: reading the key set failed (logins will retry): %v", err)
	}
	log.Printf("Identity: key set %s, access tokens only: %v", cfg.JWKSURL, policy.AccessOnly)
	return v, nil
}

// newPusher builds a sender for every configured push provider. A provider
// that fails to load is disabled with a log line; the others still work.
// Every provider that is not configured is named in the log too: staying
// silent is what once let a misspelled env var (FCM_SERVICE_ACCOUNT_KEY_PATH
// vs ..._PATH) disable push for the entire life of a deployment without
// leaving a single trace. The second result is the VAPID public key, or ""
// without Web Push.
func newPusher(cfg *Config) (*push.Dispatcher, string) {
	vapidKey := ""
	senders := map[string]push.Sender{}
	if cfg.FCMServiceAccountPath == "" {
		log.Println("Push: FCM not configured (set FCM_SERVICE_ACCOUNT_KEY_PATH)")
	} else if key, err := push.LoadServiceAccountKey(cfg.FCMServiceAccountPath); err != nil {
		logf("Push: FCM disabled: %v", err)
	} else if s, err := push.NewFCM(key, cfg.PushTimeout); err != nil {
		logf("Push: FCM disabled: %v", err)
	} else {
		senders[push.FCM] = s
	}
	if cfg.APNs.KeyPath == "" {
		log.Println("Push: APNs not configured (set IMKIT_APNS_KEY_PATH)")
	} else if s, err := push.NewAPNs(cfg.APNs); err != nil {
		logf("Push: APNs disabled: %v", err)
	} else {
		senders[push.APNs] = s
	}
	if cfg.WebPush.KeyPath == "" {
		log.Println("Push: Web Push not configured (set IMKIT_VAPID_KEY_PATH)")
	} else if s, err := push.NewWebPush(cfg.WebPush); err != nil {
		logf("Push: Web Push disabled: %v", err)
	} else {
		senders[push.WebPush] = s
		// Public: browsers subscribe with it (applicationServerKey).
		vapidKey = s.PublicKey()
		log.Printf("Push: Web Push VAPID public key %s", vapidKey)
	}
	d := push.NewDispatcher(senders)
	if d == nil {
		log.Println("Push: no provider configured; offline push disabled")
		return nil, ""
	}
	log.Printf("Push: enabled %v", d.Providers())
	return d, vapidKey
}

// warnEncryptedGroupsWithoutE2ee says so when groups were made encrypted
// under an earlier configuration: without the module nothing keeps plaintext
// out of them any more.
func warnEncryptedGroupsWithoutE2ee(store *Store) {
	n, err := store.CountEncryptedGroups()
	if err != nil {
		logf("checking for encrypted groups failed: %v", err)
		return
	}
	if n > 0 {
		log.Printf("WARNING: %d group(s) are marked encrypted but the e2ee module is not installed; their members' clients still expect encryption", n)
	}
}
