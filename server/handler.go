package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type WebSocketHandler struct {
	hub *Hub
}

func NewWebSocketHandler(hub *Hub) *WebSocketHandler {
	return &WebSocketHandler{hub: hub}
}

func (h *WebSocketHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	// Admission cap before anything else: auth in this protocol happens
	// in-band (a login message sent after the handshake, not at upgrade
	// time -- see ProcessMessage), and CheckOrigin above unconditionally
	// accepts every origin, so nothing before this point distinguishes a
	// real client from a burst of unauthenticated connection attempts.
	// Checking (and reserving) the slot before upgrading means a rejected
	// attempt never spawns the two pump goroutines a completed upgrade
	// would cost. See maxConnections's doc comment in config.go.
	if !tryAcquireConnSlot() {
		log.Printf("[WS] connection cap reached (%d), rejecting upgrade from %s", maxConnections, r.RemoteAddr)
		http.Error(w, "server busy", http.StatusServiceUnavailable)
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		log.Printf("[WS] Adding missing WebSocket upgrade headers")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade itself failed -- no Client exists to route the release
		// through Disconnect, so give the slot back here directly.
		releaseConnSlot()
		log.Printf("[WS] Upgrade error: %v", err)
		return
	}
	client := NewClient(h.hub, conn)
	log.Printf("[WS] New connection from %s", r.RemoteAddr)
	go client.WritePump()
	go client.ReadPump()
}

func (h *WebSocketHandler) RegisterUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		http.Error(w, "authorization required", http.StatusUnauthorized)
		return
	}
	token := authHeader[7:]
	appUserID, err := h.hub.auth.UserID(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	imUID, err := h.hub.store.EnsureUser(appUserID)
	if err != nil {
		http.Error(w, "create user failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"tinodeUser":"` + imUID + `","created":true}`))
}

func (h *WebSocketHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	w.Header().Set("Content-Type", "application/json")
	if err := h.hub.store.Ping(ctx); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"error","service":"im-server","error":"database unreachable"}`))
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok","service":"im-server"}`))
}

func (h *WebSocketHandler) GetTopics(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" || len(authHeader) < 7 || authHeader[:7] != "Bearer " {
		http.Error(w, "authorization required", http.StatusUnauthorized)
		return
	}
	token := authHeader[7:]
	appUserID, err := h.hub.auth.UserID(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	imUID, err := h.hub.store.GetIMUserID(appUserID)
	if err != nil {
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	topics, err := h.hub.store.GetUserTopics(imUID)
	if err != nil {
		http.Error(w, "retrieve failed", http.StatusInternalServerError)
		return
	}
	groups, _ := h.hub.store.GetUserGroups(imUID)
	resp := struct {
		Topics []TopicInfo `json:"topics"`
		Groups []GroupInfo `json:"groups"`
	}{Topics: topics, Groups: groups}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// requireInternalService gates the admin bridge used by the main Fastify API.
// These endpoints are never routed through Caddy, but are still authenticated
// so a mistakenly exposed IM port cannot become a message-history backdoor.
func (h *WebSocketHandler) requireInternalService(w http.ResponseWriter, r *http.Request) bool {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") || h.hub.auth.CheckInternalToken(strings.TrimPrefix(authHeader, "Bearer ")) != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func writeInternalJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// InternalContactSync is called by the main API whenever a friendship changes.
//
// It closes two structural gaps that im-go cannot close on its own:
//
//  1. A P2P topic row is created lazily — EnsureP2PTopic has exactly one
//     caller, resolveP2PTopic, reachable only from sub/pub/note/get/del. So
//     between "friendship accepted" and "somebody opens the chat" there is no
//     im_topics row, GetUserTopics returns nothing for the pair, and presence
//     fan-out between two brand-new friends is *structurally impossible*.
//     Materializing the topic here also means the first message no longer has
//     to pay for the cross-service AreFriends round trip (whose 3s timeout
//     hard-fails all first-contact traffic when the main server is slow).
//
//  2. Friendship lives in the main server's contacts table, which this
//     service has no database access to. Only the main API knows the event
//     happened.
//
// event=accepted materializes the topic, registers both live connections in
// the in-memory fan-out index, and nudges both clients.
// event=removed revokes both sides' im_topic_members rows (RemoveP2PMembers)
// and unsubscribes any live connections from the in-memory fan-out index, then
// nudges both clients. Message rows are deliberately left alone, because
// un-friending does not delete history — but the membership rows ARE the
// authorization gate resolveP2PTopic checks on every subsequent sub/pub/
// note/get/del/del_topic, so leaving them in place after a removal would let
// the removed party keep messaging, read full history, and even hard-delete
// the shared conversation. See RemoveP2PMembers's doc comment.
func (h *WebSocketHandler) InternalContactSync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var req struct {
		AppUserIDA string `json:"appUserIdA"`
		AppUserIDB string `json:"appUserIdB"`
		Event      string `json:"event"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	a := strings.TrimSpace(req.AppUserIDA)
	b := strings.TrimSpace(req.AppUserIDB)
	if a == "" || b == "" || a == b || len(a) > 64 || len(b) > 64 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "two distinct appUserIds required"})
		return
	}
	if req.Event != ContactEventAccepted && req.Event != ContactEventRemoved {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "event must be accepted or removed"})
		return
	}

	// EnsureUser both sides even on `removed`: it is what makes the uid ->
	// app_user_id reverse lookup (resolveAppUserID) work later, and it is
	// idempotent.
	uidA, err := h.hub.store.EnsureUser(a)
	if err != nil {
		log.Printf("[IM contact-sync] EnsureUser(A) failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "ensure user failed"})
		return
	}
	uidB, err := h.hub.store.EnsureUser(b)
	if err != nil {
		log.Printf("[IM contact-sync] EnsureUser(B) failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "ensure user failed"})
		return
	}

	topic := ""
	if req.Event == ContactEventAccepted {
		topic, err = h.hub.store.EnsureP2PTopic(uidA, uidB)
		if err != nil {
			log.Printf("[IM contact-sync] EnsureP2PTopic failed: %v", err)
			writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "ensure topic failed"})
			return
		}
		// DB membership alone does not populate h.topics, and h.topics is what
		// BroadcastToTopic and BroadcastPresence actually iterate.
		for _, uid := range []string{uidA, uidB} {
			h.hub.subscribeUser(uid, topic, nil)
		}
	} else {
		// req.Event == ContactEventRemoved: compute the canonical topic without
		// creating it (GetP2PTopic is pure) and revoke both sides' membership.
		// See RemoveP2PMembers's doc comment for why this is the actual
		// authorization gate, not just cosmetic cleanup.
		topic = h.hub.store.GetP2PTopic(uidA, uidB)
		if err := h.hub.store.RemoveP2PMembers(topic, uidA, uidB); err != nil {
			log.Printf("[IM contact-sync] RemoveP2PMembers failed: %v", err)
			writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "revoke membership failed"})
			return
		}
		// Mirror the DB change into the live fan-out index immediately -- a
		// connection that stays open across the unfriend event must stop
		// receiving broadcasts on this topic without needing to reconnect.
		for _, uid := range []string{uidA, uidB} {
			for _, c := range h.hub.clientsOf(uid) {
				c.Unsubscribe(topic)
			}
		}
	}

	h.hub.notifyContactSync(uidA, uidB, req.Event)
	writeInternalJSON(w, http.StatusOK, map[string]string{"topic": topic, "uidA": uidA, "uidB": uidB})
}

// InternalMediaRefCount reports how many live im_messages currently reference
// a media blob URL (matches either file_url or extra->>'thumbUrl' — see
// CountFileRefs's H10 doc comment for why both are checked).
//
// L1: chat file upload (POST /api/v1/chat/upload) and the message that's
// supposed to reference it are two separate, non-transactional steps — a
// crash, cancel, or exhausted send-retry between them leaves the uploaded
// blob live forever with nothing pointing at it, since nothing in this
// codebase ever swept unreferenced chat uploads. The main API's cleanup
// endpoint (DELETE /api/v1/chat/upload/:filename) can't safely delete an
// upload on its own say-so — im_messages lives entirely in this service's
// own database, so only im-go can authoritatively answer "did this ever
// actually get sent". This exists so that check can happen right before the
// delete, closing the race where the send actually succeeded in between the
// client giving up and the cleanup request arriving.
func (h *WebSocketHandler) InternalMediaRefCount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	url := strings.TrimSpace(req.URL)
	if url == "" {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "url required"})
		return
	}
	count, err := h.hub.store.CountFileRefs(url)
	if err != nil {
		log.Printf("[IM media-ref-count] CountFileRefs failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "count failed"})
		return
	}
	writeInternalJSON(w, http.StatusOK, map[string]int{"count": count})
}

// InternalTopicMembersResolve answers two related questions the main Node
// API cannot answer on its own, since im-go is the only service with access
// to group membership (im_topic_members) and the app-user-id <-> im-uid
// mapping (im_users):
//
//  1. checkAppUserIds: for each app-user id, is it currently a member of
//     this topic? Used by the group red-packet feature to verify the sender
//     (and, for lucky/equal packets, the claimant) is actually in the group.
//  2. resolveUids: for each im-go uid (the only identity the Android group
//     member picker has — a one-way hash, see IMUserID), resolve the
//     app-user id it maps to and whether that resolved user is currently a
//     group member. Used when creating an "exclusive" red packet: the
//     Android picker only ever has the recipient's uid, but
//     RedPacket.exclusiveToUserId must store the main server's AppUser.id.
//
// This sits on the money-moving path in the main API — callers there must
// fail-closed on any error from this endpoint, never treat "couldn't check"
// as "is a member".
func (h *WebSocketHandler) InternalTopicMembersResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var req struct {
		Topic           string   `json:"topic"`
		CheckAppUserIDs []string `json:"checkAppUserIds"`
		ResolveUIDs     []string `json:"resolveUids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "topic required"})
		return
	}

	members := make(map[string]bool, len(req.CheckAppUserIDs))
	for _, appUserID := range req.CheckAppUserIDs {
		appUserID = strings.TrimSpace(appUserID)
		if appUserID == "" {
			continue
		}
		uid := IMUserID(appUserID)
		ok, err := h.hub.store.IsGroupMember(topic, uid)
		if err != nil {
			log.Printf("[IM topic-members-resolve] IsGroupMember failed: %v", err)
			writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "membership check failed"})
			return
		}
		members[appUserID] = ok
	}

	uids := make([]string, 0, len(req.ResolveUIDs))
	for _, uid := range req.ResolveUIDs {
		uid = strings.TrimSpace(uid)
		if uid != "" {
			uids = append(uids, uid)
		}
	}
	// GetAppUserIDBatch already exists (built for the FCM push path's display
	// name lookups) and does exactly what the reverse direction needs here:
	// one round trip, uids with no im_users row simply absent from the map.
	appUserIDByUID, err := h.hub.store.GetAppUserIDBatch(uids)
	if err != nil {
		log.Printf("[IM topic-members-resolve] GetAppUserIDBatch failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "resolve failed"})
		return
	}
	resolved := make(map[string]map[string]interface{}, len(uids))
	for _, uid := range uids {
		appUserID, found := appUserIDByUID[uid]
		isMember := false
		if found {
			isMember, err = h.hub.store.IsGroupMember(topic, uid)
			if err != nil {
				log.Printf("[IM topic-members-resolve] IsGroupMember failed: %v", err)
				writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "membership check failed"})
				return
			}
		}
		var appUserIDValue interface{}
		if found {
			appUserIDValue = appUserID
		}
		resolved[uid] = map[string]interface{}{"appUserId": appUserIDValue, "isMember": isMember}
	}

	writeInternalJSON(w, http.StatusOK, map[string]interface{}{"members": members, "resolved": resolved})
}

// InternalRedPacketBroadcast pushes a group red packet's latest progress to
// every member currently connected to that topic, via the existing
// BroadcastToTopic fan-out — the same mechanism presence/recall/forward
// pushes already use, and the only way to reach those live sockets at all:
// they exist purely in this process's memory (h.topics), the main API has no
// way to touch them directly.
//
// The main API is the money-moving authority — this endpoint does not
// interpret payload, it only relays it inside ServerMessage.Extra (the same
// "don't bump the wire protocol for one more field" escape hatch
// SenderKind/ForwardFrom* already use).
//
// Called best-effort from the main API after a claim or an expiry settles; a
// failure here does not roll back an already-committed database change —
// members who missed the live push see the current state next time they
// batch-fetch it (see red-packets/batch on the main API).
func (h *WebSocketHandler) InternalRedPacketBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var req struct {
		Topic   string          `json:"topic"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" || len(req.Payload) == 0 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "topic and payload required"})
		return
	}
	h.hub.BroadcastToTopic(topic, &ServerMessage{Action: "red_packet_update", Topic: topic, Extra: req.Payload}, "")
	writeInternalJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// InternalCallBroadcast relays one Node-billing-driven push straight to a
// single connected user's IM socket -- the only way to reach that live
// connection at all, since it exists purely in this process's memory
// (h.clients) and the main API cannot touch it directly.
//
// Modeled on InternalRedPacketBroadcast just above (same best-effort,
// no-offline-queue, no-retry contract, same requireInternalService gate),
// but targets ONE uid instead of an entire topic's online subscribers:
// roubao's per-call billing tick (imCallBilling.ts, IM_VOICE_VIDEO_CALL_PLAN.md
// §4.4/§5) calls this once per recipient it needs to reach -- e.g.
// credit_balance to only the caller every ~60s, or call_ended{reason:
// "credit_exhausted"} to caller AND callee as two separate calls -- rather
// than fanning out to a topic the way a group red packet update does.
//
// Payload is relayed byte-for-byte via SendRaw, exactly as opaque here as
// InternalRedPacketBroadcast's Extra field is to it ("the main API is the
// money-moving authority" — that function's own doc comment). roubao is
// expected to have already shaped payload as the exact wire frame the
// target client understands on its own (e.g. {"type":"credit_balance",
// "credits":123}, or the very same CallSignalMessage JSON this file sends
// for call_ended/call_ring/... elsewhere in hub.go), so the client's
// existing dispatch-by-"type"/"action" logic needs no special case for "this
// one arrived via the bridge instead of directly from a WS handler".
//
// Unlike a topic broadcast, "not reachable right now" here means there is
// genuinely nobody to tell: this feature has no offline queue for call
// signaling at all (plan §1 decision 4, §4.3) -- a credit_balance tick or a
// call_ended the target's socket cannot currently take is not something
// replaying it later would make sense of; roubao's own periodic sweep (plan
// §5's last bullet), not this endpoint, is the authoritative backstop for a
// call whose live signaling silently failed to reach someone.
func (h *WebSocketHandler) InternalCallBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var req struct {
		TargetUID string          `json:"targetUid"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	targetUID := strings.TrimSpace(req.TargetUID)
	if targetUID == "" || len(req.Payload) == 0 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "targetUid and payload required"})
		return
	}
	// Every device of the target: a balance or a call ending concerns each.
	delivered := false
	for _, target := range h.hub.clientsOf(targetUID) {
		target.SendRaw(req.Payload)
		delivered = true
	}
	// roubao proactively ending a call (credit exhausted mid-call, max
	// duration reached, or roubao judging this im-go process itself
	// unreachable -- see imCallBilling.ts's runTick()) relays a call_ended
	// payload through this exact endpoint but never goes through any of the
	// WS handlers in this file that normally clear h.hub.calls (handleCallHangup,
	// handleCallReject, ...). Without this, the busy slot this call reserved
	// under both participants' uids (callHub.reserve, hub.go) stays stuck
	// until one of their sockets disconnects or the process restarts -- see
	// releaseCallSlotForBroadcastEnd's own doc comment for why this can't
	// just reuse forceEndCallForDisconnect. Delivery reachability above is
	// irrelevant to this: roubao has already decided the call is over on its
	// side regardless of whether the target happened to be online to receive
	// this particular push.
	var typed struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(req.Payload, &typed) == nil {
		h.hub.mods.hostBroadcast(h.hub, targetUID, typed.Type)
	}
	writeInternalJSON(w, http.StatusOK, map[string]bool{"ok": true, "delivered": delivered})
}

// InternalAdminTopics lists compact summaries for a known IM UID. The caller
// is the main API, which resolves that UID from an authenticated admin's user
// search and maps peer UIDs back to public profile data.
func (h *WebSocketHandler) InternalAdminTopics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	uid := strings.TrimSpace(r.URL.Query().Get("uid"))
	if !strings.HasPrefix(uid, "uid_") || len(uid) > 64 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "valid uid required"})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	topics, err := h.hub.store.GetAdminTopics(uid, limit)
	if err != nil {
		log.Printf("[IM admin] list topics failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "retrieve failed"})
		return
	}
	writeInternalJSON(w, http.StatusOK, map[string]interface{}{"topics": topics})
}

// InternalAdminTopic returns membership and clear state for a single topic.
func (h *WebSocketHandler) InternalAdminTopic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	topic := strings.TrimSpace(r.URL.Query().Get("topic"))
	if topic == "" || len(topic) > 128 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "topic required"})
		return
	}
	info, err := h.hub.store.GetAdminTopic(topic)
	if err != nil {
		writeInternalJSON(w, http.StatusNotFound, map[string]string{"error": "topic not found"})
		return
	}
	writeInternalJSON(w, http.StatusOK, info)
}

// InternalAdminMessages pages IM history with filters already applied in the
// IM database. It intentionally requires a topic from the main API rather
// than accepting an arbitrary SQL expression or raw filter object.
func (h *WebSocketHandler) InternalAdminMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	q := r.URL.Query()
	topic := strings.TrimSpace(q.Get("topic"))
	if topic == "" || len(topic) > 128 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "topic required"})
		return
	}
	beforeSeq, _ := strconv.Atoi(q.Get("beforeSeq"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	fromMs, _ := strconv.ParseInt(q.Get("fromMs"), 10, 64)
	toMs, _ := strconv.ParseInt(q.Get("toMs"), 10, 64)
	kind := q.Get("type")
	if kind != "" && kind != "text" && kind != "image" && kind != "video" && kind != "audio" && kind != "file" {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message type"})
		return
	}
	messages, hasMore, err := h.hub.store.GetAdminMessages(topic, beforeSeq, limit, kind, strings.TrimSpace(q.Get("keyword")), fromMs, toMs)
	if err != nil {
		log.Printf("[IM admin] list messages failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "retrieve failed"})
		return
	}
	writeInternalJSON(w, http.StatusOK, map[string]interface{}{"messages": messages, "hasMore": hasMore})
}

// InternalAdminClearTopic invokes the same transactional server-side clear
// path used by IM clients, but broadcasts to both participants because this
// action was initiated by an administrator instead of either user.
func (h *WebSocketHandler) InternalAdminClearTopic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.requireInternalService(w, r) {
		return
	}
	var body struct {
		Topic string `json:"topic"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	topic := strings.TrimSpace(body.Topic)
	if topic == "" || len(topic) > 128 {
		writeInternalJSON(w, http.StatusBadRequest, map[string]string{"error": "topic required"})
		return
	}
	result, err := h.hub.ClearDirectTopicForAdmin(topic)
	if err != nil {
		if strings.Contains(err.Error(), "two-person") {
			writeInternalJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "not found") {
			writeInternalJSON(w, http.StatusNotFound, map[string]string{"error": "topic not found"})
			return
		}
		log.Printf("[IM admin] clear topic failed: %v", err)
		writeInternalJSON(w, http.StatusInternalServerError, map[string]string{"error": "clear failed"})
		return
	}
	writeInternalJSON(w, http.StatusOK, result)
}
