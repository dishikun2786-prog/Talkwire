package main

import (
	"fmt"
	"log"
	"net/http"
	"time"
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

	// Initialize tables
	if err := store.InitTables(); err != nil {
		log.Fatalf("Failed to initialize tables: %v", err)
	}

	// Initialize auth service
	auth := NewAuthService(cfg.JWTSecret)

	// Load FCM service account key for offline push, if configured
	var fcmKey *ServiceAccountKey
	if cfg.FCMServiceAccountPath != "" {
		key, err := LoadServiceAccountKey(cfg.FCMServiceAccountPath)
		if err != nil {
			logf("FCM service account load failed (push notifications disabled): %v", err)
		} else {
			fcmKey = key
			log.Println("FCM push notifications enabled")
		}
	} else {
		// Say so out loud. Staying silent here is what let a misspelled env var
		// (FCM_SERVICE_ACCOUNT_KEY_PATH vs ..._PATH) disable push for the entire
		// life of the deployment without leaving a single trace in the log.
		log.Println("FCM not configured (set FCM_SERVICE_ACCOUNT_KEY_PATH); push notifications disabled")
	}

	// Initialize internal API client (friendship checks against the main server)
	internalAPI := NewInternalAPIClient(cfg.MainAPIBaseURL, cfg.JWTSecret)

	// Initialize hub
	hub := NewHub(store, auth, fcmKey, internalAPI)
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
	// Same shape again for encrypted payloads queued for offline delivery.
	go store.StartE2eEnvelopeReaper()
	// Keeps the group-chat limits (member cap, message length, forward
	// batch sizes, ...) in sync with the main server's config registry.
	// Started after the hub is already running: the service is fully
	// functional on the built-in defaults, so a slow or unreachable main
	// server must not hold up accepting connections.
	go internalAPI.StartRuntimeConfigRefresh(runtimeConfigRefreshInterval)
	// Same rationale, separate cycle — see E2eeRuntimeConfig's doc comment
	// for why this isn't folded into the group-chat one above.
	go internalAPI.StartE2eeConfigRefresh(runtimeConfigRefreshInterval)

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
