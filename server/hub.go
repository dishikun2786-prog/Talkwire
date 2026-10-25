package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"math/rand"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Hub maintains the set of active clients and routes messages between them
type Hub struct {
	store       *Store
	auth        *AuthService
	fcm         *ServiceAccountKey            // nil disables push notifications
	internalAPI *InternalAPIClient            // friendship checks + sender profile lookups against the main server
	clients     map[string]*Client            // uid -> client (one active session per user)
	topics      map[string]map[string]*Client // topic -> uid -> client, for O(topic size) broadcast fan-out
	mu          sync.RWMutex
	register    chan *Client
	unregister  chan *Client

	// presenceCh serializes presence fan-out through a single worker. See
	// runPresenceWorker for why two bare `go BroadcastPresence(...)` calls
	// were not safe.
	presenceCh chan presenceEvent

	// presRev gives every presence frame a per-uid monotonic revision so a
	// client can discard an out-of-order one. Seeded from presEpoch (the hub's
	// start time in ms) so revisions keep increasing across an im-go restart —
	// otherwise a restart would reset counters to 1 and every client holding a
	// higher revision from the previous process would reject all new presence.
	// Its own small mutex: read from client goroutines (snapshots) and written
	// by the presence worker, and it has no reason to contend with h.mu.
	presRevMu sync.Mutex
	presRev   map[string]int64
	presEpoch int64

	// groupAiSettingsCache (topic -> groupAiSettingsCacheEntry) and
	// forwardReqCache (reqId -> cachedForwardResult) are independent
	// read-through caches; sync.Map handles its own locking so they don't
	// need the hub's mu.
	groupAiSettingsCache sync.Map
	forwardReqCache      sync.Map

	// groupNameCache (topic -> groupNameCacheEntry) backs groupNameCached.
	// announceMessage now stamps the group name onto every group data frame
	// (F-31), which without a cache would mean two extra Postgres queries
	// (GetGroupInfo does a SELECT plus a COUNT) on the hot path of every
	// single group message. Same shape as the two caches above: sync.Map
	// handles its own locking, zero value is usable, no NewHub init needed.
	groupNameCache sync.Map

	// calls tracks every ringing/answering/active IM voice/video call
	// (IM_VOICE_VIDEO_CALL_PLAN.md §4.3). A dedicated struct with its own
	// mutex, not fields guarded by h.mu -- call bookkeeping never needs to
	// coordinate atomically with topic subscription/client registration, and
	// h.mu is already a carefully lock-hold-time-optimized hot path (see
	// broadcastRaw's own comment on why marshal happens before locking); this
	// is the same "independent lock, not sharing h.mu" choice already made
	// for groupAiSettingsCache/forwardReqCache/pubRateLimiter/aiCooldownMap.
	calls *callHub

	// pendingCallDisconnects (uid -> *time.Timer) backs
	// scheduleForceEndCallForDisconnect's reconnect grace period: a real
	// disconnect while a call is ringing/answering/active does not
	// immediately force-end it, it schedules the force-end and remembers
	// the timer here so the register branch can cancel it if this exact uid
	// comes back before it fires. sync.Map, not h.mu -- same "independent
	// lock" reasoning as groupAiSettingsCache/forwardReqCache above; this
	// has nothing to coordinate with topic/client bookkeeping either.
	pendingCallDisconnects sync.Map
}

// callDisconnectGraceDuration is how long forceEndCallForDisconnect waits
// before actually ending a call after a participant's socket drops, giving a
// same-process reconnect (mobile OEM background management tearing down and
// rebuilding the WebSocket -- VIVO OriginOS's proprietary background
// throttling is the confirmed real-world trigger, see the Android-side
// investigation this fixes) a window to land first. ImWebSocketClient's own
// reconnect backoff starts at 1.5s (CN) / 3s (intl) before jitter, so this
// needs meaningful headroom past that first attempt's connect+login round
// trip on a throttled mobile link, not just the backoff delay itself.
// Deliberately NOT the ~30-45s a genuine "callee never answers" ring timeout
// uses -- this path only fires for a uid that WAS just live a moment ago, so
// a long wait here only prolongs the caller's "ringing" for no benefit once
// it's clear the callee truly isn't coming back this session.
//
// var, not const, following fcmHTTPTimeout's own envDuration precedent just
// above in this file: operationally tunable without a rebuild via
// IM_CALL_DISCONNECT_GRACE_SEC, and package tests can lower it directly
// (save/restore around the test) instead of waiting out a real 10s timer.
var callDisconnectGraceDuration = envDuration("IM_CALL_DISCONNECT_GRACE_SEC", 10*time.Second)

func NewHub(store *Store, auth *AuthService, fcm *ServiceAccountKey, internalAPI *InternalAPIClient) *Hub {
	return &Hub{
		store:       store,
		auth:        auth,
		fcm:         fcm,
		internalAPI: internalAPI,
		clients:     make(map[string]*Client),
		topics:      make(map[string]map[string]*Client),
		register:    make(chan *Client, 100),
		unregister:  make(chan *Client, 100),
		// Deep enough that ordinary mobile-network churn never reaches the
		// drop path; enqueuePresence deliberately drops rather than blocking
		// the hub loop if it ever does.
		presenceCh: make(chan presenceEvent, 1024),
		presRev:    make(map[string]int64),
		presEpoch:  time.Now().UnixMilli(),
		calls:      newCallHub(),
	}
}

// nextPresRev bumps and returns the revision for a presence transition.
func (h *Hub) nextPresRev(uid string) int64 {
	h.presRevMu.Lock()
	defer h.presRevMu.Unlock()
	cur, ok := h.presRev[uid]
	if !ok {
		cur = h.presEpoch
	}
	cur++
	h.presRev[uid] = cur
	return cur
}

// currentPresRev returns the revision a snapshot should carry: the last
// transition's revision, or the epoch itself if this uid has never
// transitioned in this process. Every revision is therefore >= presEpoch, so a
// snapshot is always ordered at or below the first real transition and the
// client's "discard if rev < lastRev" rule is total.
func (h *Hub) currentPresRev(uid string) int64 {
	h.presRevMu.Lock()
	defer h.presRevMu.Unlock()
	if cur, ok := h.presRev[uid]; ok {
		return cur
	}
	return h.presEpoch
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			var victim *Client
			h.mu.Lock()
			if existing, ok := h.clients[client.uid]; ok && existing != client {
				victim = existing
			}
			h.clients[client.uid] = client
			h.mu.Unlock()

			// This uid is live again -- cancel any scheduleForceEndCallForDisconnect
			// grace timer a previous disconnect left pending for it. Runs
			// unconditionally on every register, not just reconnects with a
			// victim below: a brand new login for a uid that was never in a
			// call is simply a no-op inside cancelPendingCallDisconnect.
			h.cancelPendingCallDisconnect(client.uid)

			// Disconnect() blocking-sends on h.unregister, and the ONLY
			// receiver of that channel is this very Run loop. Calling it
			// inline — worse, while holding h.mu, as this used to — lets a
			// mass-reconnect storm fill the 100-slot buffer and wedge Run
			// forever *while holding the global lock*: subscribeTopic,
			// BroadcastToTopic, BroadcastPresence and GetClientByUID all
			// stall behind it and the whole service goes silently dead with
			// no error and no log. Hand the kick to another goroutine and
			// never hold the lock across it. (conn.Close() inside Disconnect
			// is a blocking syscall too, which is the second reason.)
			if victim != nil {
				logf("Kicking existing session for %s", client.uid)
				go func(v *Client) {
					v.SendJSON(&ServerMessage{
						Action: "login",
						Code:   CodeAuth,
						Reason: "logged in from another device",
					})
					v.Disconnect()
				}(victim)
			}

			h.enqueuePresence(client.uid, PresOnline)
			logf("Client registered: %s (total: %d)", client.uid, len(h.clients))

		case client := <-h.unregister:
			topics := client.drainSubscriptions()
			h.mu.Lock()
			// A reconnecting user briefly has two live *Client objects for one
			// uid, and the superseded one unregisters *after* the replacement
			// has already registered and re-subscribed. So every cleanup below
			// must key on connection IDENTITY, never on the uid string alone.
			// (h.clients[uid] missing entirely also lands here: either the
			// replacement already unregistered, or this client never got in —
			// both mean "not the live connection", which is what we want.)
			superseded := h.clients[client.uid] != client
			if !superseded {
				delete(h.clients, client.uid)
			}
			for _, topic := range topics {
				if m, ok := h.topics[topic]; ok {
					// Unguarded, this deleted by uid and therefore evicted the
					// *replacement* connection from every shared topic. Since
					// h.topics is the sole fan-out source for both
					// BroadcastToTopic and BroadcastPresence, the survivor then
					// silently lost real-time messages AND presence while still
					// believing it was subscribed (its `sub` had returned 200,
					// and the client's knownTopics guard suppresses any re-sub),
					// with only a full re-login as repair.
					if m[client.uid] == client {
						delete(m, client.uid)
						if len(m) == 0 {
							delete(h.topics, topic)
						}
					}
				}
			}
			h.mu.Unlock()

			if client.uid == "" {
				break
			}
			if superseded {
				// Announcing "offline" here is how a user who had just
				// reconnected got shown as offline to all their peers, with
				// nothing in the system able to correct it afterwards.
				logf("Client unregistered (superseded by newer session, presence unchanged): %s", client.uid)
			} else {
				h.enqueuePresence(client.uid, PresOffline)
				logf("Client unregistered: %s (total: %d)", client.uid, len(h.clients))
				// Deliberately inside the !superseded branch, same "connection
				// identity, not uid string" discipline as the h.topics cleanup
				// a few lines up (IM_VOICE_VIDEO_CALL_PLAN.md §4.3): a mere
				// reconnect (new connection already registered under this uid,
				// old one now unwinding through here as the victim) must NOT
				// force-end a call this uid is legitimately still in -- only a
				// genuine "this uid has no live connection left at all" should.
				// callState never holds a *Client, only uid strings, so the
				// call survives the reconnect untouched either way; this only
				// runs at all when the uid is truly gone -- and even then,
				// not immediately: scheduleForceEndCallForDisconnect gives a
				// real reconnect (OEM background-throttling teardown/rebuild,
				// not just this instant-supersede case) a grace period to
				// land before actually ending anything. See its own doc
				// comment for why the ~5-second-scale window this teardown/
				// rebuild takes on some OEM skins can't be caught by the
				// supersede check above alone.
				h.scheduleForceEndCallForDisconnect(client.uid)
			}
		}
	}
}

func (h *Hub) ProcessMessage(client *Client, msg *ClientMessage) {
	switch msg.Action {
	case ActionLogin:
		h.handleLogin(client, msg)
	case ActionSub:
		h.handleSub(client, msg)
	case ActionPub:
		h.handlePub(client, msg)
	case ActionNote:
		h.handleNote(client, msg)
	case ActionLeave:
		h.handleLeave(client, msg)
	case ActionGet:
		h.handleGet(client, msg)
	case ActionPres:
		h.handlePresQuery(client, msg)
	case ActionDel:
		h.handleDel(client, msg)
	case ActionDelTopic:
		h.handleDelTopic(client, msg)
	case ActionCreateGrp:
		h.handleCreateGrp(client, msg)
	case ActionInvite:
		h.handleInvite(client, msg)
	case ActionKick:
		h.handleKick(client, msg)
	case ActionLeaveGrp:
		h.handleLeaveGrp(client, msg)
	case ActionUpdateGrp:
		h.handleUpdateGrp(client, msg)
	case ActionTransferOwner:
		h.handleTransferOwner(client, msg)
	case ActionSetMemberRole:
		h.handleSetMemberRole(client, msg)
	case ActionDisbandGrp:
		h.handleDisbandGrp(client, msg)
	case ActionGetGrps:
		h.handleGetGrps(client, msg)
	case ActionGetMbrs:
		h.handleGetMbrs(client, msg)
	case ActionRegToken:
		h.handleRegToken(client, msg)
	case ActionUnregToken:
		h.handleUnregToken(client, msg)
	case ActionForwardBundle:
		h.handleForwardBundle(client, msg)
	case ActionForwardBroadcast:
		h.handleForwardBroadcast(client, msg)
	case ActionGetGroupAiSettings:
		h.handleGetGroupAiSettings(client, msg)
	case ActionUpdateGroupAiSettings:
		h.handleUpdateGroupAiSettings(client, msg)
	case ActionAckTopicCleared:
		h.handleAckTopicCleared(client, msg)
	case actionAckGroupEvent:
		h.handleAckPendingGroupEvent(client, msg)
	case ActionGroupQrToken:
		h.handleGroupQrToken(client, msg)
	case ActionGroupQrResolve:
		h.handleGroupQrResolve(client, msg)
	case ActionGroupJoin:
		h.handleGroupJoin(client, msg)
	case ActionGroupJoinRequestsList:
		h.handleGroupJoinRequestsList(client, msg)
	case ActionGroupJoinReview:
		h.handleGroupJoinReview(client, msg)
	case ActionGetGroupJoinSettings:
		h.handleGetGroupJoinSettings(client, msg)
	case ActionUpdateGroupJoinSettings:
		h.handleUpdateGroupJoinSettings(client, msg)
	case ActionE2ePublishBundle:
		h.handleE2ePublishBundle(client, msg)
	case ActionE2eFetchBundle:
		h.handleE2eFetchBundle(client, msg)
	case ActionE2eOtpkCount:
		h.handleE2eOtpkCount(client, msg)
	case ActionE2eSend:
		h.handleE2eSend(client, msg)
	case ActionE2eTerminate:
		h.handleE2eTerminate(client, msg)
	case ActionE2eClear:
		h.handleE2eClear(client, msg)
	case ActionE2eGroupClear:
		h.handleE2eGroupClear(client, msg)
	case ActionE2eSetGroupEncrypted:
		h.handleE2eSetGroupEncrypted(client, msg)
	case ActionCallInvite:
		h.handleCallInvite(client, msg)
	case ActionCallAccept:
		h.handleCallAccept(client, msg)
	case ActionCallReject:
		h.handleCallReject(client, msg)
	case ActionCallCancel:
		h.handleCallCancel(client, msg)
	case ActionCallHangup:
		h.handleCallHangup(client, msg)
	case ActionCallUpgrade:
		h.handleCallUpgrade(client, msg)
	case ActionCallUpgradeAccept:
		h.handleCallUpgradeAccept(client, msg)
	case ActionCallUpgradeReject:
		h.handleCallUpgradeReject(client, msg)
	default:
		client.SendJSON(ErrorResponse(msg.Id, msg.Action, CodeBadReq, "unknown action: "+msg.Action))
	}
}

func (h *Hub) handleLogin(client *Client, msg *ClientMessage) {
	// M19: without this guard, a client sending a second "login" on an
	// already-authenticated connection overwrites client.uid in place.
	// h.register's `existing != client` check (see Run()) only catches a
	// DIFFERENT connection claiming a uid that's already registered — it
	// does nothing to protect against the SAME connection switching
	// identities, because the old uid was never re-registered through that
	// path to begin with. The old uid's h.clients entry and every
	// h.topics[topic][oldUid] subscription this connection made stay
	// indexed under the abandoned identity forever: eventual disconnect
	// cleanup (h.unregister) only looks under client.uid's CURRENT value,
	// so nothing ever revisits them. A stale h.clients[oldUid] entry then
	// makes GetClientByUID(oldUid) return this connection as if that user
	// were still online and reachable here — messages/pushes meant for the
	// old identity could be delivered down a socket now authenticated as
	// someone else entirely.
	if client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeBadReq, "already logged in"))
		return
	}
	if msg.Token == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeAuth, "token required"))
		return
	}
	appUserID, err := h.auth.GetAppUserID(msg.Token)
	if err != nil {
		logf("Login failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeAuth, "invalid token"))
		return
	}
	imUID, err := h.store.EnsureUser(appUserID)
	if err != nil {
		logf("EnsureUser failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionLogin, CodeInternal, "server error"))
		return
	}
	// 本次审查顺带发现并修复：这两行此前都是绕过 c.mu 的裸字段赋值，即使
	// UID()/setUID() 已经存在（F-39 的修复），登录处理本身却从未真正改用它——
	// 意味着 F-39 只修了"读"，没有修"写"，读侧的锁保护不了一个仍然不加锁的
	// 写入者，本质上还是同一类数据竞争。appUserID 是本次通话功能新增的字段，
	// 一并按 setUID/AppUserID() 的同款模式补上 setAppUserID()（见 client.go）。
	client.setUID(imUID)
	client.setAppUserID(appUserID)
	client.loginTime = time.Now()
	h.register <- client
	client.SendJSON(&ServerMessage{Action: ActionLogin, Code: CodeOK, UID: imUID})
	logf("User logged in: %s -> %s", appUserID, imUID)
	go h.deliverPendingClears(client)
	go h.deliverPendingGroupEvents(client)
	go h.deliverPendingE2eEnvelopes(client)
}

// deliverPendingE2eEnvelopes replays every encrypted payload that arrived
// for this uid while they were offline, exactly mirroring the shape a live
// e2e_send relay would have used — see handleE2eSend. Runs off a goroutine
// like its im_pending_clears/im_pending_group_events counterparts so a
// slow query never delays the login ack itself.
func (h *Hub) deliverPendingE2eEnvelopes(client *Client) {
	pending, err := h.store.PopE2eEnvelopesForUID(client.uid)
	if err != nil {
		logf("deliverPendingE2eEnvelopes: query failed for %s: %v", client.uid, err)
		return
	}
	for _, e := range pending {
		client.SendJSON(&ServerMessage{
			Action: ActionE2eDeliver, Topic: e.Topic, From: e.FromUID,
			E2eKind: e.Kind, E2eMsgType: e.MsgType, Content: e.Payload,
			Timestamp: time.Now().UnixMilli(),
		})
	}
}

// deliverPendingClears replays every clear this uid missed while offline,
// exactly mirroring the shape broadcastTopicCleared would have sent had they
// been online. Runs on a `go` goroutine off handleLogin so a slow/failing
// query never delays the login ack itself.
func (h *Hub) deliverPendingClears(client *Client) {
	pending, err := h.store.GetPendingClears(client.uid)
	if err != nil {
		logf("deliverPendingClears: query failed for %s: %v", client.uid, err)
		return
	}
	for _, p := range pending {
		client.SendJSON(&ServerMessage{
			Id: strconv.FormatInt(p.ID, 10), Action: ActionTopicCleared,
			Topic: p.WireTopic, From: p.FromUID,
			ClearVersion: p.ClearVersion, ClearBeforeSeq: p.ClearBeforeSeq,
		})
	}
}

// handleAckTopicCleared is fire-and-forget: the client has already applied
// the clear locally by the time it sends this, so whether or not the delete
// succeeds only affects when the row is finally reclaimed, never correctness.
func (h *Hub) handleAckTopicCleared(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Id == "" {
		return
	}
	id, err := strconv.ParseInt(msg.Id, 10, 64)
	if err != nil {
		return
	}
	if err := h.store.AckPendingClear(id, client.uid); err != nil {
		logf("handleAckTopicCleared: %v", err)
	}
}

// actionAckGroupEvent 是客户端对一条补投递群事件的回执。
//
// 用非导出常量而不是加进 protocol.go 的 Action* 表：那个文件由另一路负责，
// 本文件不能改它，而这里必须有一个可用的判据。命名刻意小写以免与将来可能
// 加进 protocol.go 的 ActionAckGroupEvent 撞名。
const actionAckGroupEvent = "ack_group_event"

// pendingGroupEventPush 是从离线队列补投递出去的一条群事件。
//
// 帧形与在线时收到的 grp_sync / grp_disband 逐字一致（action/topic/name/from），
// 只多一个 eventId —— 老客户端会直接忽略这个多出来的键，新客户端拿它回
// ack_group_event 把队列行销掉。不复用 ServerMessage 是因为它没有 eventId
// 字段，而 protocol.go 不归本文件改；也刻意不挪用 ServerMessage.Id：那个字段
// 是请求-响应关联用的，补投递帧不是任何请求的响应，占用它会让客户端把这条
// 推送错配到某个在飞请求上（ImWebSocketClient 找不到精确 id 时会按 action
// 前缀兜底挑一个在飞请求，正是本仓库踩过的坑）。
type pendingGroupEventPush struct {
	Action  string `json:"action"`
	Topic   string `json:"topic"`
	Name    string `json:"name,omitempty"`
	From    string `json:"from,omitempty"`
	EventId int64  `json:"eventId"`
	// JoinStatus rides along only on a replayed group_join_reviewed push
	// (PendingGroupEventJoinApproved/Rejected) -- empty and omitted for the
	// pre-existing grp_sync/grp_disband kinds.
	JoinStatus string `json:"joinStatus,omitempty"`
}

// enqueueGroupEventForOffline 给此刻**不在线**的成员各留一条持久事件，等他们
// 下次登录由 deliverPendingGroupEvents 补投。在线成员刚刚已经通过 WS 收到过
// 同一事件，不入队。
//
// 与 broadcastTopicCleared 的离线分支是同一套设计：群成员关系的变化（进群、
// 被踢、群解散）在服务端是一次性事件，成员行改完就没有任何后续流量会顺带
// 纠正一个当时不在线的人的本地状态。
func (h *Hub) enqueueGroupEventForOffline(topic string, uids []string, fromUID, kind, groupName string) {
	for _, uid := range uids {
		if uid == "" || uid == fromUID || h.GetClientByUID(uid) != nil {
			continue
		}
		if err := h.store.EnqueuePendingGroupEvent(topic, uid, kind, groupName, fromUID); err != nil {
			logf("enqueueGroupEventForOffline: topic=%s uid=%s kind=%s: %v", topic, uid, kind, err)
		}
	}
}

// deliverPendingGroupEvents 重放这个 uid 离线期间错过的群事件。与
// deliverPendingClears 并列，在 handleLogin 末尾以 `go` 启动，慢查询不拖累
// 登录 ack 本身。
func (h *Hub) deliverPendingGroupEvents(client *Client) {
	pending, err := h.store.GetPendingGroupEvents(client.uid)
	if err != nil {
		logf("deliverPendingGroupEvents: query failed for %s: %v", client.uid, err)
		return
	}
	for _, ev := range pending {
		switch ev.Kind {
		case ActionGrpSync:
			// 从入队到这个人上线之间，他可能已经被踢、或者群已经解散，这条
			// 同步就过期了。此时既不该推（端侧会凭空多出一个进不去的群），也
			// 不该留（永远重放）—— 直接销掉。
			isMember, memErr := h.store.IsGroupMember(ev.Topic, client.uid)
			if memErr != nil {
				// 查不动就原样留着下次登录再试，绝不当成「已不是成员」误删。
				logf("deliverPendingGroupEvents: membership check failed topic=%s uid=%s: %v", ev.Topic, client.uid, memErr)
				continue
			}
			if !isMember {
				if ackErr := h.store.AckPendingGroupEvent(ev.ID, client.uid); ackErr != nil {
					logf("deliverPendingGroupEvents: drop stale event %d: %v", ev.ID, ackErr)
				}
				continue
			}
			// 与在线邀请路径同一个理由：端侧的 handleGroupSync 只把 topic 记进
			// knownTopics 而**不订阅**，服务端不主动把它放进 h.topics 的话，
			// 这个群的实时消息他一条都收不到（离线推送又会因为他此刻在线而跳过）。
			client.Subscribe(ev.Topic)
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGrpSync, Topic: ev.Topic, Name: ev.GroupName,
				From: ev.FromUID, EventId: ev.ID,
			})
		case ActionGrpDisband:
			client.Unsubscribe(ev.Topic)
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGrpDisband, Topic: ev.Topic,
				From: ev.FromUID, EventId: ev.ID,
			})
		case PendingGroupEventJoinRequest:
			// No re-check of ensureGroupManager here -- if this uid's role
			// changed since the request was queued, the real authorization
			// gate is ensureGroupManager on their next group_join_requests_list
			// call, not this "go check" nudge.
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGroupJoinRequestPush, Topic: ev.Topic, Name: ev.GroupName,
				From: ev.FromUID, EventId: ev.ID,
			})
		case PendingGroupEventJoinApproved, PendingGroupEventJoinRejected:
			status := "approved"
			if ev.Kind == PendingGroupEventJoinRejected {
				status = "rejected"
			}
			client.SendAny(&pendingGroupEventPush{
				Action: ActionGroupJoinReviewed, Topic: ev.Topic, Name: ev.GroupName,
				EventId: ev.ID, JoinStatus: status,
			})
		default:
			logf("deliverPendingGroupEvents: unknown kind %q on event %d, dropping", ev.Kind, ev.ID)
			if ackErr := h.store.AckPendingGroupEvent(ev.ID, client.uid); ackErr != nil {
				logf("deliverPendingGroupEvents: drop unknown event %d: %v", ev.ID, ackErr)
			}
		}
	}
}

// handleAckPendingGroupEvent 与 handleAckTopicCleared 一样是 fire-and-forget：
// 客户端发这条时本地状态已经改完了，删不删得掉队列行只影响它什么时候被回收，
// 不影响正确性（下次登录重放一遍是幂等的）。
func (h *Hub) handleAckPendingGroupEvent(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.EventID <= 0 {
		return
	}
	if err := h.store.AckPendingGroupEvent(msg.EventID, client.uid); err != nil {
		logf("handleAckPendingGroupEvent: %v", err)
	}
}

func (h *Hub) handleSub(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionSub, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionSub, CodeBadReq, "topic required"))
		return
	}
	if msg.Topic == TopicMe {
		client.Subscribe(msg.Topic)
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionSub, Code: CodeOK, Topic: msg.Topic})
		return
	}
	if msg.Topic == TopicFnd {
		client.SendJSON(ErrorResponse(msg.Id, ActionSub, CodeBadReq, "fnd not supported"))
		return
	}

	// P2P topics are addressed by the peer's UID on the wire, but broadcasts
	// are matched against the canonical p2p_<hash> topic (see handlePub/handleGet).
	// Without this resolution, subscribing here recorded the subscription under
	// the wrong key and the peer never received real-time messages/presence
	// until they happened to pub/get in the same session.
	actualTopic := msg.Topic
	isP2P := isUserUID(msg.Topic)
	peerOnline := false
	if isP2P {
		var ok bool
		actualTopic, peerOnline, ok = h.resolveP2PTopic(client, msg.Id, ActionSub, msg.Topic)
		if !ok {
			return
		}
	} else if !h.ensureTopicMember(client, msg, ActionSub, actualTopic) {
		return
	}

	client.Subscribe(actualTopic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionSub, Code: CodeOK, Topic: msg.Topic})
	if isP2P {
		// Presence is otherwise only pushed at the instant a peer connects or
		// disconnects, and only to clients already online at that moment — a
		// client subscribing later would never learn the peer's current
		// status. Send an explicit snapshot right after subscribing.
		status := PresOffline
		if peerOnline {
			status = PresOnline
		}
		client.SendJSON(&ServerMessage{
			Action: ActionPres, Topic: msg.Topic, What: status,
			Online: peerOnline, PresRev: h.currentPresRev(msg.Topic),
		})
	}
	logf("[Sub] %s -> %s (%s)", client.uid, msg.Topic, actualTopic)
}

func (h *Hub) handlePub(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeAuth, "not authenticated"))
		return
	}
	// 消息发送频率兜底：handlePub 此前只校验长度，完全没有速率限制——一个失控
	// 或恶意的客户端可以按网络能承受的最快速度往任意会话灌消息。按 uid 而不是
	// 按 topic 限，否则同一个人开更多会话就能把有效速率乘上去；1:1 与群聊
	// 共用同一计数，两边都是需要防的场景。这是一道安全网，不是产品级节流——
	// 默认值（每10秒20条）远高于正常聊天的实际速度。
	if !tryAcquirePublish(client.uid, CurrentRuntimeConfig().PublishRateLimitPer10s, pubRateWindow) {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeRateLimited, "发送太快，请稍后再试"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeBadReq, "topic required"))
		return
	}
	if msg.Topic == TopicMe || msg.Topic == TopicFnd {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeBadReq, "cannot publish to me/fnd"))
		return
	}
	// 上限改为运行期可配（group_chat.message_max_len），不再是编译期常量：
	// 调这个值以前必须重新出包并重启 im-go。
	if utf8.RuneCountInString(msg.Content) > CurrentRuntimeConfig().MessageMaxLen {
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeBadReq, "message too long"))
		return
	}

	topic := msg.Topic
	var actualTopic string

	if isUserUID(topic) {
		var ok bool
		actualTopic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionPub, topic)
		if !ok {
			return
		}
	} else {
		actualTopic = topic
	}

	// 非 uid 形态的 topic 每次都要校验。改前有三个各自独立的缺口：只在「尚未订阅」
	// 时查（订阅过一次就永久免检）、只覆盖 grp_ 前缀（canonical p2p_ 可直接往别人的
	// 1:1 会话注入消息）、以及 err == nil && !isMember 在查询出错时放行。
	if !isUserUID(topic) && !h.ensureTopicMember(client, msg, ActionPub, actualTopic) {
		return
	}
	// Encrypted groups must never accept a plain pub: the whole point of
	// e2e_set_group_encrypted is that from that moment on, every member's
	// client believes this topic's content is unreadable server-side, and a
	// plaintext pub landing in im_messages — client bug, stale/unencrypted
	// build, or a deliberately malicious client — would be a silent breach
	// of that belief for everyone else in the group. 1:1 encrypted threads
	// need no equivalent check here: they live under a wholly separate
	// e2e_ topic namespace that resolveP2PTopic never produces, so there is
	// no shared topic id for a plain pub to collide with in the first place.
	if strings.HasPrefix(actualTopic, "grp_") {
		if encrypted, err := h.store.IsGroupEncrypted(actualTopic); err == nil && encrypted {
			client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeConflict, "group_encrypted"))
			return
		}
	}
	if !client.IsSubscribed(actualTopic) {
		client.Subscribe(actualTopic)
	}

	var extra json.RawMessage
	if msg.Extra != nil && string(msg.Extra) != "null" {
		extra = msg.Extra
	}

	seq, tsMillis, duplicate, err := h.deliverMessage(actualTopic, client.uid, SenderKindUser, msg.Content, msg.ContentType, msg.FileURL, msg.FileName, extra, msg.Duration, nil, msg.ClientMsgId)
	if err != nil {
		logf("Save message failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeInternal, "save failed"))
		return
	}

	// TOCTOU close: the check at line ~612 and this write are not one atomic
	// operation, so a concurrent e2e_set_group_encrypted commit in that
	// narrow window would have let a plaintext message land in im_messages
	// for a group everyone now believes is encrypted. Re-checking a SECOND
	// time right after the write, and self-correcting via the existing
	// recall path if the race actually happened, turns "silently and
	// permanently wrong" into "corrected within milliseconds and the sender
	// is told to retry" — not a perfect atomic guarantee (that would need
	// deliverMessage itself, shared by forwards/AI replies/every plain send,
	// to take a per-topic advisory lock spanning both the check and the
	// write), but the actual exposure window shrinks from "indefinite" to
	// "this one request's own round trip," which for a group message that
	// literally just got flagged encrypted moments ago is effectively closed.
	if !duplicate && strings.HasPrefix(actualTopic, "grp_") {
		if encrypted, err := h.store.IsGroupEncrypted(actualTopic); err == nil && encrypted {
			if _, _, delErr := h.store.SoftDeleteMessage(actualTopic, seq, client.uid); delErr != nil {
				logf("handlePub: TOCTOU self-correction failed to recall seq=%d topic=%s: %v", seq, actualTopic, delErr)
			}
			client.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeConflict, "group_encrypted"))
			return
		}
	}

	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionPub, Code: CodeOK,
		Topic: msg.Topic, Seq: seq,
		Timestamp: tsMillis, Extra: extra,
	})

	if duplicate {
		// H3: this pub carried a clientMsgId already saved earlier -- a
		// resend of one whose ack the client never saw, not a new message.
		// The sender already got a correct ack above; nothing else (no
		// re-broadcast, no re-triggered AI QA dispatch) should happen a
		// second time for content everyone already received once.
		return
	}

	// @小桃子 group AI QA detection. This is the ONLY place that ever calls
	// dispatchAiQaTask from message content — deliverMessage itself (used by
	// forwards and by the AI's own replies) never scans content for mentions.
	// That's a structural isolation, not a runtime senderKind check: an AI
	// reply, or a forwarded copy of one, physically cannot re-trigger this
	// because it never passes through handlePub at all (see plan §8.3/§9.2.3).
	if strings.HasPrefix(actualTopic, "grp_") && hasValidAiMention(msg.Content) {
		go h.dispatchAiQaTask(actualTopic, client.uid, msg.Content)
	}
}

// ForwardMeta carries forward provenance to attach to a newly delivered
// message; nil means the message being delivered is not a forward.
type ForwardMeta struct {
	FromMsgID  int
	FromTopic  string
	ChainDepth int
}

// deliverMessage is the single entry point shared by every path that writes
// a message into a topic and needs it to actually reach people: user
// publishes (handlePub above), broadcast-mode forwards
// (handleForwardBroadcast), and AI QA replies (dispatchAiQaTask). It assigns
// a seq, persists the row, broadcasts it to online subscribers, and kicks off
// (async) FCM delivery to offline members — the four steps handlePub used to
// inline entirely by itself, which meant every other writer either had to
// duplicate them or silently skip the offline-push step (see plan §9.2.1).
//
// clientMsgID (H3): non-empty only from handlePub. When set, the save goes
// through Store.SaveMessageFullIdempotent instead of SaveMessageFull -- a
// resend of a pub whose ack the client never saw (connection dropped
// mid-round-trip; see ImWebSocketClient.kt's PUBLISH_PENDING handling)
// returns the ORIGINAL seq/duplicate=true instead of creating a second
// message with a new seq. duplicate=true also skips the broadcast/FCM step
// below -- everyone who was going to see this message already did, the
// first time. Forward and AI-reply callers always pass "" and always get
// duplicate=false, i.e. this is a no-op change for them.
//
// handleForwardBundle is the one caller that cannot go through this
// function at all: its N inserts must share a single DB transaction, so it
// calls the *Tx store primitives directly and then reuses announceMessage
// (this function's broadcast+push half) once per row after COMMIT.
func (h *Hub) deliverMessage(topic, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardMeta *ForwardMeta, clientMsgID string) (seq int, tsMillis int64, duplicate bool, err error) {
	var forwardFromMsgID *int
	var forwardFromTopic *string
	chainDepth := 0
	if forwardMeta != nil {
		id := forwardMeta.FromMsgID
		forwardFromMsgID = &id
		topicCopy := forwardMeta.FromTopic
		forwardFromTopic = &topicCopy
		chainDepth = forwardMeta.ChainDepth
	}
	if clientMsgID != "" {
		var dup bool
		var savedAt time.Time
		seq, savedAt, dup, err = h.store.SaveMessageFullIdempotent(topic, clientMsgID, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, chainDepth)
		if err != nil {
			return 0, 0, false, err
		}
		tsMillis = savedAt.UnixMilli()
		if dup {
			// Already broadcast the first time this clientMsgID was saved --
			// re-announcing now would re-deliver to every online member and
			// re-trigger AI QA dispatch a second time for the same @mention.
			// The caller (handlePub) still gets a correct seq+ts to ack with:
			// savedAt is the ORIGINAL insert time, not now() -- using now()
			// here would hand the client a timestamp materially later than
			// what every other participant already sorted this message at.
			return seq, tsMillis, true, nil
		}
	} else {
		seq, err = h.store.SaveMessageFull(topic, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, chainDepth)
		if err != nil {
			return 0, 0, false, err
		}
		tsMillis = time.Now().UnixMilli()
	}
	h.announceMessage(topic, seq, fromUID, senderKind, content, contentType, fileURL, fileName, extra, duration, forwardFromMsgID, forwardFromTopic, clientMsgID)
	return seq, tsMillis, false, nil
}

// announceMessage broadcasts an already-persisted message to online
// subscribers and asynchronously pushes FCM to offline members. Split out of
// deliverMessage purely so handleForwardBundle can reuse it after its own
// transaction commits — see deliverMessage's doc comment.
func (h *Hub) announceMessage(topic string, seq int, fromUID, senderKind, content, contentType, fileURL, fileName string, extra json.RawMessage, duration int, forwardFromMsgID *int, forwardFromTopic *string, clientMsgID string) {
	// The `topic` argument is the CANONICAL topic id and is what the fan-out
	// below must route on. It is not what the payload may carry for P2P.
	//
	// Android keys a 1:1 conversation by the PEER'S UID, and normalizes an
	// incoming data frame with:
	//     topicName = if (rawTopic == myUserId && from.isNotBlank()) from else rawTopic
	// (ImWebSocketClient.kt:880). Putting the canonical "p2p_<hash>" on the
	// wire makes that branch never fire, so the message files under a topicId
	// nothing in the app ever queries and simply never appears — no Android
	// code recognizes the p2p_ prefix at all. handleGet and handleDel both
	// still answer with the client's own wire topic, which is why history and
	// recall kept working while only the realtime push regressed.
	//
	// BroadcastToTopic excludes the sender, so for a P2P topic every recipient
	// is by definition the other party, and their conversation key is exactly
	// fromUID. Deriving it here rather than echoing whatever the sender wrote
	// also makes the payload correct for the forward paths, which address
	// their target by canonical topic id.
	payloadTopic := topic
	if strings.HasPrefix(topic, "p2p_") {
		payloadTopic = fromUID
	}
	dataMsg := &ServerMessage{
		Action: ActionData, Topic: payloadTopic, From: fromUID,
		Seq: seq, Content: content, ContentType: contentType,
		FileURL: fileURL, FileName: fileName,
		Duration: duration, Timestamp: time.Now().UnixMilli(), Extra: extra,
		SenderKind: senderKind,
	}
	if forwardFromMsgID != nil {
		dataMsg.ForwardFromMsgID = *forwardFromMsgID
	}
	if forwardFromTopic != nil {
		dataMsg.ForwardFromTopic = *forwardFromTopic
	}
	if clientMsgID != "" {
		dataMsg.ClientMsgId = clientMsgID
	}
	// F-31：群消息的 data 帧改前不带群名。端侧收到一条来自尚未出现在本地会话
	// 列表里的 grp_ 会话的推送时，手上只有 topic 串和 from，会话标题只能先显示
	// 成 topic 号，必须再单独拉一次群列表才知道这是哪个群。
	// ServerMessage 里没有独立的 groupName 字段（protocol.go 不归本文件改），
	// 复用既有的 Name —— 它在 data 帧里本来恒为空，语义上不冲突。
	if strings.HasPrefix(topic, "grp_") {
		dataMsg.Name = h.groupNameCached(topic)
	}
	h.BroadcastToTopic(topic, dataMsg, fromUID)
	go h.pushOfflineRecipients(topic, fromUID, senderKind, content, contentType)
}

// fcmPushConcurrency is the fallback bound on how many FCM sends run in
// parallel per message, used only when the runtime config hasn't produced a
// usable value. The live value is group_chat.fcm_concurrency; this constant
// exists purely so make(chan struct{}, n) can never be handed a n <= 0
// (a negative capacity panics, and 0 silently serializes the whole fan-out).
const fcmPushConcurrency = 8

// pushOfflineRecipients sends an FCM push to topic members who are not
// currently connected (BroadcastToTopic only reaches online WS clients).
// Sends fan out concurrently (bounded) instead of one-at-a-time — a big
// group previously meant a fully serial chain of HTTPS calls to FCM before
// the last offline member got notified.
//
// senderKind == SenderKindAiAssistant skips the im_users lookup entirely and
// uses a fixed "小桃子" name — the reserved AI uid is never written to
// im_users, so GetAppUserID(senderUID) would fail every time otherwise (this
// is exactly what made offline push for AI replies silently no-op before —
// see plan §9.2.1).
func (h *Hub) pushOfflineRecipients(topic, senderUID, senderKind, content, contentType string) {
	rc := CurrentRuntimeConfig()
	// 运营侧的总开关（group_chat.fcm_enabled）。FCM 出问题时以前只能改服务环境
	// 变量再重启整个 im-go，等于把所有人的实时连接一起断掉。
	if !rc.FCMEnabled {
		return
	}
	if h.fcm == nil {
		return
	}
	memberUIDs, err := h.store.GetTopicMemberUIDs(topic)
	if err != nil {
		logf("pushOfflineRecipients: GetTopicMemberUIDs failed: %v", err)
		return
	}

	// F-14：先把「真正需要推」的离线成员筛出来，再一次性批量查 token 与
	// appUserId。改前是在成员循环里逐个 GetFCMTokens + GetAppUserID，一条群
	// 消息 = 2N 次数据库往返；500 人的群里发一句话就是上千次查询，而这段代码
	// 跑在每条消息的 announceMessage 之后。
	offline := make([]string, 0, len(memberUIDs))
	for _, uid := range memberUIDs {
		if uid == senderUID || h.GetClientByUID(uid) != nil {
			continue
		}
		offline = append(offline, uid)
	}
	if len(offline) == 0 {
		return
	}

	tokensByUID, err := h.store.GetFCMTokensBatch(offline)
	if err != nil {
		logf("pushOfflineRecipients: GetFCMTokensBatch failed: %v", err)
		return
	}

	body := previewText(content, contentType)
	isGroup := strings.HasPrefix(topic, "grp")
	groupName := ""
	if isGroup {
		groupName = h.groupNameCached(topic)
	}

	var senderAppUserID string
	if senderKind != SenderKindAiAssistant {
		senderAppUserID, err = h.store.GetAppUserID(senderUID)
		if err != nil {
			// 改前这里直接 return —— 发件人 appUserId 只用来解析「在收件人眼里
			// 这个发件人叫什么名字」（备注名优先），查不到最多是标题显示成
			// 「用户」，不该让整条离线通知链路一个人都推不出去。
			logf("pushOfflineRecipients: GetAppUserID(sender) failed, falling back to generic name: %v", err)
			senderAppUserID = ""
		}
	}

	// 只有真要解析昵称时才批量查收件人的 appUserId。
	appUserIDs := map[string]string{}
	if senderKind != SenderKindAiAssistant && senderAppUserID != "" {
		appUserIDs, err = h.store.GetAppUserIDBatch(offline)
		if err != nil {
			logf("pushOfflineRecipients: GetAppUserIDBatch failed: %v", err)
			appUserIDs = map[string]string{}
		}
	}

	concurrency := rc.FCMConcurrency
	if concurrency <= 0 {
		concurrency = fcmPushConcurrency
	}
	// M16: token/appUserId lookups above are already batched (F-14) — this
	// loop is what's left, and GetProfile is still one HTTP round trip to the
	// main server PER RECIPIENT, run synchronously in this loop before that
	// recipient's own FCM sends are even dispatched. It can't be batched away
	// like the DB lookups were: it's genuinely per-viewer (a recipient may
	// have set a personal alias for the sender, see Contact.alias), so each
	// call legitimately needs its own request. But the N calls are
	// independent of each other, and running them one-at-a-time meant a large
	// group's last offline member's push waited on every other member's round
	// trip finishing first. Each recipient's full pipeline (profile
	// resolution + that recipient's own token sends) now runs in one
	// goroutine, bounded by the same semaphore that used to bound only the
	// FCM-send step — a recipient's own multiple device tokens are still sent
	// sequentially within its goroutine (most users have one device), which
	// keeps concurrent HTTPS requests to Google bounded by roughly
	// `concurrency` the same way the original fcmPushConcurrency comment
	// intended, just now also covering the previously-serial profile lookups.
	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for _, uid := range offline {
		tokens := tokensByUID[uid]
		if len(tokens) == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(uid string, tokens []string) {
			defer wg.Done()
			defer func() { <-sem }()

			senderName := "用户"
			senderAvatar := ""
			if senderKind == SenderKindAiAssistant {
				senderName = "小桃子"
			} else if senderAppUserID != "" {
				if viewerAppUserID := appUserIDs[uid]; viewerAppUserID != "" {
					name, avatar, gerr := h.internalAPI.GetProfile(viewerAppUserID, senderAppUserID)
					if gerr != nil {
						logf("pushOfflineRecipients: GetProfile failed: %v", gerr)
					} else {
						if name != "" {
							senderName = name
						}
						senderAvatar = avatar
					}
				}
			}

			// 端侧对 1:1 会话的本地键是**对端的 uid**（实时推送在 announceMessage 里也是
			// 这么改写 payloadTopic 的），而这里原样下发的是服务端内部的 canonical
			// p2p_<hash>。点击这条通知进去的会话因此挂在一个端侧永远读不到的幽灵 key 上：
			// 会话列表里看不见、历史再重复落一份、转账记录的 topic 也是错的。
			// 对收件人来说这个会话的对端就是发送方，故填 senderUID。
			// 群聊的 grp_ 前缀两侧命名一致，保持原样。
			pushTopic := topic
			if !isGroup {
				pushTopic = senderUID
			}
			data := map[string]string{
				"topic":      pushTopic,
				"senderName": senderName,
				"preview":    body,
				"isGroup":    strconv.FormatBool(isGroup),
			}
			if senderAvatar != "" {
				data["senderAvatar"] = senderAvatar
			}
			if isGroup && groupName != "" {
				data["groupName"] = groupName
			}
			for _, token := range tokens {
				if err := sendFCMv1(h.fcm, token, data); err != nil {
					logf("FCM push failed for %s: %v", uid, err)
					if errors.Is(err, ErrFCMTokenUnregistered) {
						if delErr := h.store.DeleteFCMTokenByValue(token); delErr != nil {
							logf("DeleteFCMTokenByValue failed: %v", delErr)
						}
					}
				}
			}
		}(uid, tokens)
	}
	wg.Wait()
}

func previewText(content, contentType string) string {
	if content != "" {
		runes := []rune(content)
		if len(runes) > 60 {
			return string(runes[:60]) + "..."
		}
		return content
	}
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return "[图片]"
	case strings.HasPrefix(contentType, "video/"):
		return "[视频]"
	case strings.HasPrefix(contentType, "audio/"):
		return "[语音]"
	default:
		return "[附件]"
	}
}

func (h *Hub) handleNote(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" || msg.What == "" {
		return
	}
	noteMsg := &ServerMessage{
		Action: ActionInfo, Topic: msg.Topic,
		From: client.uid, What: msg.What, Seq: msg.Seq,
	}
	var topic string
	if isUserUID(msg.Topic) {
		var ok bool
		topic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionNote, msg.Topic)
		if !ok {
			return
		}
	} else {
		topic = msg.Topic
		// F-13：`note` 是输入中/已读这类高频信号（每次按键一条），原来每条都要
		// 打一次 DB 做成员校验。h.topics 里存在这条**连接本身**（按身份比对，不
		// 只是 uid 相同）就说明它此前已经通过 sub/get/pub 的授权闸门才被
		// Client.Subscribe 登记进索引 —— Subscribe 是 h.topics 唯一的写入点。
		// 拿它当已授权的快路径，不在索引里才回落到真正的 DB 校验。
		// 已知残留：被解除好友时 RemoveP2PMembers 只删 DB 行、不摘在线连接的
		// 订阅，所以对方在重连之前仍可能收到一条「正在输入」。只有输入态信号、
		// 且重连即自愈，用它换掉每次按键一条查询是划算的。
		if !h.isSubscribedInIndex(topic, client) && !h.ensureTopicMember(client, msg, ActionNote, topic) {
			return
		}
	}
	// Group read receipts: persist the sender's read cursor before relaying,
	// scoped to grp_ topics and gated by group_chat.read_receipt_enabled
	// (reused from the existing, previously Android-bootstrap-only flag --
	// see RuntimeConfig). P2P already has its own single-recipient READ
	// status client-side and is deliberately left alone. Only "read" notes
	// hit the DB here, not every "kp" keypress -- see the F-13 note above
	// this function on why "kp" stays on the DB-free fast path.
	if msg.What == NoteRead && strings.HasPrefix(topic, "grp_") && CurrentRuntimeConfig().ReadReceiptEnabled {
		if err := h.store.UpsertReadCursor(topic, client.uid, msg.Seq); err != nil {
			logf("UpsertReadCursor(%s,%s,%d): %v", topic, client.uid, msg.Seq, err)
		}
	}
	h.BroadcastToTopic(topic, noteMsg, client.uid)
}

func (h *Hub) handleLeave(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" {
		return
	}
	client.Unsubscribe(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionLeave, Code: CodeOK, Topic: msg.Topic})
}

func (h *Hub) handleGet(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeBadReq, "topic required"))
		return
	}

	// 默认页大小与硬上限都从运行期配置来（group_chat.history_page_size，
	// 注册表把它约束在 20..200）。越界一律回落到默认值，而不是钳到上限——
	// 一个要 100000 条的客户端多半是算错了，给它 200 条同样不是它想要的。
	//
	// 这段判据此前是就地写在这里的，而 store.GetMessages/GetMessagesAfter 里
	// 另有一套各自硬编码的 `>100 就压成 50`，跑在这一层之后：101..200 在这里
	// 放行、到 store 被静默改成 50，运营把 history_page_size 调到 100 以上完全
	// 无效。现在唯一的归一化实现在 config.go 的 normalizeHistoryLimit，这里和
	// store 层都调它，不要再在任何一层写第二套上限。
	limit := normalizeHistoryLimit(msg.Limit)

	var topic string
	isP2P := isUserUID(msg.Topic)
	peerOnline := false
	if isP2P {
		var ok bool
		topic, peerOnline, ok = h.resolveP2PTopic(client, msg.Id, ActionGet, msg.Topic)
		if !ok {
			return
		}
	} else {
		topic = msg.Topic
		if !h.ensureTopicMember(client, msg, ActionGet, topic) {
			return
		}
	}

	var msgs []StoredMessage
	var err error
	if msg.AfterSeq > 0 {
		msgs, err = h.store.GetMessagesAfter(topic, msg.AfterSeq, limit)
	} else {
		msgs, err = h.store.GetMessages(topic, msg.BeforeSeq, limit)
	}
	if err != nil {
		logf("Get messages failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeInternal, "retrieve failed"))
		return
	}
	clearState, err := h.store.GetAdminTopic(topic)
	if err != nil {
		logf("Get topic clear state failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionGet, CodeInternal, "retrieve failed"))
		return
	}

	// Read-cursor baseline: checked by prefix, not isP2P, because a client
	// can also `get` a P2P topic by its already-resolved p2p_ topic string
	// (isUserUID(msg.Topic) is false in that case), which must not be
	// mistaken for a group. Omitted entirely (nil -> omitempty) unless the
	// feature is on, so a disabled deployment sends exactly the same
	// response shape as before this field existed.
	var readCursors []ReadCursorDto
	if strings.HasPrefix(topic, "grp_") && CurrentRuntimeConfig().ReadReceiptEnabled {
		cursors, cerr := h.store.GetReadCursors(topic)
		if cerr != nil {
			logf("GetReadCursors(%s): %v", topic, cerr)
		} else {
			readCursors = make([]ReadCursorDto, len(cursors))
			for i, c := range cursors {
				readCursors[i] = ReadCursorDto{UID: c.UID, Seq: c.Seq}
			}
		}
	}

	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionGet, Code: CodeOK, Topic: msg.Topic, Messages: msgs,
		ClearVersion: clearState.ClearVersion, ClearBeforeSeq: clearState.ClearBeforeSeq,
		ReadCursors: readCursors,
	})
	client.Subscribe(topic)
	// handleGet subscribes exactly like handleSub, but used to skip the
	// presence snapshot handleSub sends. Loading history is the path a client
	// actually takes once its process-lifetime knownTopics set has started
	// suppressing re-subscribes, so the topic got registered for delivery
	// while the peer's status was never reported at all.
	if isP2P {
		status := PresOffline
		if peerOnline {
			status = PresOnline
		}
		client.SendJSON(&ServerMessage{
			Action: ActionPres, Topic: msg.Topic, What: status,
			Online: peerOnline, PresRev: h.currentPresRev(msg.Topic),
		})
	}
}

func (h *Hub) handleDel(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.Seq <= 0 {
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeBadReq, "topic and seq required"))
		return
	}
	// Resolve a P2P peer-UID topic to the canonical stored topic, mirroring
	// handlePub — messages live under p2p_<hash>, not the peer's raw UID, so
	// the old code (which deleted by the raw peer UID) silently matched nothing.
	actualTopic := msg.Topic
	if isUserUID(msg.Topic) {
		var ok bool
		actualTopic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionDel, msg.Topic)
		if !ok {
			return
		}
	} else if !h.ensureTopicMember(client, msg, ActionDel, actualTopic) {
		// The canonical-topic (grp_/p2p_) branch had no membership check at
		// all -- only the uid_ branch above got one, via resolveP2PTopic.
		// The recall primitive's from_uid=caller scoping means the practical
		// damage was capped at "can only recall your own messages", but that still
		// let a non-member (or a former member kicked from the group) probe
		// arbitrary topic strings and, worse, recall a message they'd sent
		// before being removed -- retroactively altering history other
		// members still see. Same pattern handleDelTopic already uses for
		// this same class of gap.
		return
	}
	// F-15：改为软删（置 recalled_at，行保留）。硬删会把 (topic, seq) 这个位置
	// 从序列里抠掉，而端侧的离线补拉是「给我 seq > 本地最大值的消息」，序列一旦
	// 出现空洞就再也无法判断「这里本来有一条被撤回了」还是「这条我漏收了」。
	// 保留行让 seq 始终稠密，读取侧靠 recalled_at IS NULL 过滤。
	//
	// Own-messages-only recall: scoped to from_uid = caller, so a client can
	// never recall the other party's messages.
	fileURL, deleted, err := h.store.SoftDeleteMessage(actualTopic, msg.Seq, client.uid)
	if err != nil {
		logf("Delete failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeInternal, "delete failed"))
		return
	}
	if !deleted {
		// F-34：改前无论有没有真的撤回都先回 200 再判断。客户端据此把本地那条
		// 消息也删掉了，于是「撤回一条不属于自己的 seq」「撤回一条已经不存在的
		// seq」都会造成本地少一条、服务端还留着的静默不一致。
		client.SendJSON(ErrorResponse(msg.Id, ActionDel, CodeNotFound, "message not found or not yours"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionDel, Code: CodeOK, Topic: msg.Topic, Seq: msg.Seq})
	// Push the recall to the other online participant(s) so their copy vanishes
	// with no trace (distinct "recalled" action, addressed exactly like a data
	// push so the client's P2P-topic normalization files it correctly).
	//
	// Offline peers self-heal, but not for the reason this comment used to
	// give ("the row is already gone"): since the switch to soft delete the row
	// very much survives, as a tombstone with recalled_at set and its content
	// fields stripped. What omits it from their next fetch is the
	// `recalled_at IS NULL` predicate on every read path (GetMessages /
	// GetMessagesAfter). The row staying put is the point -- it keeps the
	// (topic, seq) series dense so gap detection does not chase it forever.
	h.BroadcastToTopic(actualTopic, &ServerMessage{Action: ActionRecalled, Topic: msg.Topic, Seq: msg.Seq, From: client.uid}, client.uid)
	// gcMediaBlob 自己会先 CountFileRefs，被别处（转发副本）引用就不删。软删
	// 的墓碑行虽然留在表里，但 file_url 已被置 NULL，所以不会把自己的 blob 钉住。
	// ⚠️ 与硬删时期的 H10 修复不对等：视频消息的首帧缩略图存在 extra->>'thumbUrl'
	// 里，SoftDeleteMessage 按跨端契约只返回一个 URL，那个缩略图 blob 因此收不
	// 回来（详见 store.go 上该函数的注释）。
	if fileURL != "" {
		go h.gcMediaBlob(fileURL)
	}
}

// handleDelTopic clears an entire conversation for everyone: every message in
// the topic is hard-deleted and its orphaned media blobs removed, then the
// other participant is told to clear their local copy too.
func (h *Hub) handleDelTopic(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionDelTopic, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionDelTopic, CodeBadReq, "topic required"))
		return
	}
	actualTopic := msg.Topic
	if isUserUID(msg.Topic) {
		var ok bool
		actualTopic, _, ok = h.resolveP2PTopic(client, msg.Id, ActionDelTopic, msg.Topic)
		if !ok {
			return
		}
	} else if strings.HasPrefix(actualTopic, "grp_") {
		// F-01/D1：群聊的「清空聊天记录」是给**所有人**硬删全部消息并回收媒体，
		// 不可逆。改前只要求「是群成员」，等于群里任何一个人都能一键抹掉整群的
		// 历史。收敛为群主或管理员。
		// 1:1 会话不走这条分支：那里双方本来就都是「自己会话的主人」，清空是
		// 双方共有的权利。但此前这里写的「仍由下面的 ensureTopicMember 兜住」
		// 只对三分之一的情形成立，实际是三条分支各管一段：
		//   - uid_/usr 形态：在上面第一个分支就被 resolveP2PTopic 消费掉了，
		//     授权来自它内部的 IsGroupMember/AreFriends，根本走不到下面那行；
		//   - grp_ 前缀：本分支，收敛为群主或管理员；
		//   - 裸 p2p_ 串：只有这一种才真的落到下面的 ensureTopicMember。
		if !h.ensureGroupManager(client, msg, ActionDelTopic, actualTopic) {
			return
		}
	} else if !h.ensureTopicMember(client, msg, ActionDelTopic, actualTopic) {
		// 改前这个分支只覆盖 grp_ 前缀，裸 p2p_ 串完全没有授权分支——而这条路径是
		// 不可逆的第三方数据销毁（硬删 im_messages + 回收媒体）。
		return
	}
	result, err := h.store.ClearTopicMessages(actualTopic)
	if err != nil {
		logf("Delete topic failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionDelTopic, CodeInternal, "clear failed"))
		return
	}
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionDelTopic, Code: CodeOK, Topic: msg.Topic,
		ClearVersion: result.ClearVersion, ClearBeforeSeq: result.ClearBeforeSeq,
		DeletedMessages: result.DeletedMessages, DeletedMedia: result.DeletedMedia,
	})
	members, memberErr := h.store.GetTopicMemberUIDs(actualTopic)
	if memberErr != nil {
		logf("Clear topic member lookup failed: %v", memberErr)
	} else {
		h.broadcastTopicCleared(actualTopic, members, client.uid, result)
	}
	for _, u := range result.FileURLs {
		go h.gcMediaBlob(u)
	}
}

// ClearDirectTopicForAdmin is the privileged counterpart to handleDelTopic.
// It is deliberately exported only within the im-go process and called from a
// shared-secret HTTP handler; the main API must never delete im_messages
// directly because online clients need the same topic_cleared event.
func (h *Hub) ClearDirectTopicForAdmin(topic string) (ClearTopicResult, error) {
	info, err := h.store.GetAdminTopic(topic)
	if err != nil {
		return ClearTopicResult{}, err
	}
	if info.IsGroup || len(info.Members) != 2 {
		return ClearTopicResult{}, fmt.Errorf("only two-person direct conversations can be cleared by admin")
	}
	result, err := h.store.ClearTopicMessages(topic)
	if err != nil {
		return ClearTopicResult{}, err
	}
	// Admin-initiated deletion reaches both participants, including users who
	// are currently on the conversation screen. Offline clients reconcile the
	// clear version during their next history pull.
	h.broadcastTopicCleared(topic, info.Members, "", result)
	for _, u := range result.FileURLs {
		go h.gcMediaBlob(u)
	}
	return result, nil
}

// broadcastTopicCleared sends directly to active members instead of relying
// on topic subscriptions: a connected client may not currently have this
// older conversation subscribed, but its Room cache must still be cleared.
// P2P UI keys use the peer UID while database topics use p2p_<hash>, so each
// target receives its own peer UID as the wire topic.
//
// Members who are offline at this moment get a durable per-member tombstone
// instead (im_pending_clears) -- delivered and acked the next time they log
// in (see handleLogin -> deliverPendingClears / handleAckTopicCleared).
func (h *Hub) broadcastTopicCleared(topic string, members []string, excludeUID string, result ClearTopicResult) {
	// Group topics are always "grp_"-prefixed (CreateGroup); P2P topics are
	// "p2p_"-prefixed (GetP2PTopic) -- a structural fact about topic naming,
	// not a guess from headcount. The old len(members)==2 check would
	// misclassify a 2-person GROUP as P2P.
	isP2P := !strings.HasPrefix(topic, "grp_")
	for _, uid := range members {
		if uid == excludeUID {
			continue
		}
		wireTopic := topic
		if isP2P {
			for _, peerUID := range members {
				if peerUID != uid {
					wireTopic = peerUID
					break
				}
			}
		}
		target := h.GetClientByUID(uid)
		if target == nil {
			if err := h.store.EnqueuePendingClear(topic, uid, wireTopic, result.ClearVersion, result.ClearBeforeSeq, excludeUID); err != nil {
				logf("broadcastTopicCleared: enqueue pending clear failed for topic=%s uid=%s: %v", topic, uid, err)
			}
			continue
		}
		target.SendJSON(&ServerMessage{
			Action: ActionTopicCleared, Topic: wireTopic, From: excludeUID,
			ClearVersion: result.ClearVersion, ClearBeforeSeq: result.ClearBeforeSeq,
		})
	}
}

// gcMediaBlob asks the main server to delete a media blob, but only once no
// remaining message references it (media can be shared across chats via
// forwarding, which reuses the same file URL).
func (h *Hub) gcMediaBlob(fileURL string) {
	if h.internalAPI == nil || fileURL == "" {
		return
	}
	n, err := h.store.CountFileRefs(fileURL)
	if err != nil {
		logf("gcMediaBlob CountFileRefs failed: %v", err)
		return
	}
	if n > 0 {
		return // still referenced by another message
	}
	if err := h.internalAPI.DeleteMedia([]string{fileURL}); err != nil {
		logf("gcMediaBlob DeleteMedia failed: %v", err)
	}
}

func (h *Hub) handleCreateGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeAuth, "not authenticated"))
		return
	}
	rc := CurrentRuntimeConfig()
	if strings.TrimSpace(msg.Name) == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeBadReq, "group name required"))
		return
	}
	if utf8.RuneCountInString(msg.Name) > rc.NameMaxLen {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeBadReq, "群名称过长"))
		return
	}
	// 先去重再算人数：CreateGroup 内部靠 ON CONFLICT DO NOTHING 吞掉重复项，
	// 拿原始 slice 的长度做上限判断会被「同一个 uid 报 500 次」直接绕过。
	// 顺带剔掉空串与群主自己，让 len(members)+1 就是真实入群人数；也让下面
	// 那圈 AreFriends 不会对同一个人重复发起 HTTP 往返。
	seen := make(map[string]bool, len(msg.Members))
	members := make([]string, 0, len(msg.Members))
	for _, m := range msg.Members {
		if m == "" || m == client.uid || seen[m] {
			continue
		}
		seen[m] = true
		members = append(members, m)
	}
	if len(members)+1 > rc.MaxMembers {
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeBadReq, "群成员数量超出上限"))
		return
	}
	// M14: neither this handler nor handleInvite validated ANY relationship
	// between the caller and the members being added — only that the caller
	// was authenticated (and, for invite, already a group member). Any
	// authenticated user who merely knew another user's uid could add them
	// to a brand-new group with zero consent or relationship check. Mirrors
	// resolveP2PTopic's identical AreFriends gate — im-go has no local
	// knowledge of the contacts table, so this has to be a round trip to the
	// main server (see internal_client.go's doc comment on why).
	for _, uid := range members {
		if uid == client.uid {
			continue
		}
		memberAppUserID, err := h.resolveAppUserID(uid)
		if err != nil {
			logf("resolveAppUserID(%s) failed: %v", uid, err)
			client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeAuth, "cannot verify relationship"))
			return
		}
		friends, err := h.internalAPI.AreFriends(client.appUserID, memberAppUserID)
		if err != nil {
			logf("AreFriends check failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeInternal, "relationship check failed"))
			return
		}
		if !friends {
			client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeAuth, "not friends: "+uid))
			return
		}
	}
	topic, err := h.store.CreateGroup(client.uid, msg.Name, members)
	if err != nil {
		logf("Create group failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionCreateGrp, CodeInternal, "create failed"))
		return
	}
	client.Subscribe(topic)
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionCreateGrp, Code: CodeCreated, Topic: topic, Name: msg.Name,
	})
	logf("[Group] Created %s by %s: %s", topic, client.uid, msg.Name)

	// Notify initial members who are currently online so their conversation
	// list picks the group up immediately instead of waiting for their next
	// unrelated refresh/reconnect.
	offline := make([]string, 0, len(members))
	for _, uid := range members {
		if target := h.GetClientByUID(uid); target != nil {
			// DB membership alone does not put them in h.topics, which is what
			// BroadcastToTopic actually iterates — and pushOfflineRecipients
			// skips anyone holding a live connection, so without this an online
			// member got the group's messages via neither WS nor FCM. Exactly
			// the reason syncP2PPeer exists on the 1:1 path.
			target.Subscribe(topic)
			target.SendJSON(&ServerMessage{Action: ActionGrpSync, Topic: topic, Name: msg.Name, From: client.uid})
			continue
		}
		offline = append(offline, uid)
	}
	// F-04：建群时不在线的成员改前什么都收不到。DB 里有成员行，但端侧要等到
	// 下一次 get_groups 才可能发现自己进了这个群；在那之前群里发的消息对他
	// 既没有实时推送（不在 h.topics 里）也进不了会话列表。留一条持久事件，
	// 下次登录由 deliverPendingGroupEvents 补投。
	h.enqueueGroupEventForOffline(topic, offline, client.uid, ActionGrpSync, msg.Name)
}

func (h *Hub) handleInvite(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeBadReq, "topic and user required"))
		return
	}
	// F-03：邀请只对群聊成立。改前完全没有这个判据，而 AddGroupMember 不看
	// is_group —— 任何人只要拿到一条 1:1 会话的 canonical p2p_ 串，就能往
	// im_topic_members 里插进第三个人；那个人从此就是「成员」：能订阅实时
	// 消息、能拉走全部历史、能发言。ensureTopicMember 之后也拦不住他，因为
	// 他此刻确实有成员行。
	if !strings.HasPrefix(msg.Topic, "grp_") {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeBadReq, "only group topics can be invited into"))
		return
	}
	// Any current member may invite others (mirrors WeChat's default group
	// behavior) — this used to be owner-only.
	isMember, err := h.store.IsGroupMember(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeNotFound, "group not found"))
		return
	}
	if !isMember {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "only group members can invite"))
		return
	}
	// 目标已经在群里就幂等返回。
	//
	// 这里此前的注释写的是「必须在所有分支之前短路，否则会落到 AddGroupMember，
	// 那条语句是 ON CONFLICT DO UPDATE SET role = 'member'，重复邀请一位管理员
	// 会把他悄悄降级」——那个越权降级属实，但已经在 store 层根治了：
	// AddGroupMember 现在是 ON CONFLICT (topic, uid) DO NOTHING（见该函数注释），
	// 角色变更只能走 SetGroupMemberRole。所以本处短路**不再**是安全边界，
	// 只是省掉一次注定 DO NOTHING 的写库和一轮无谓的好友校验。
	//
	// 仍然要放在好友校验之前：否则「已经在同一个群里、但后来解除了好友关系」
	// 会被误判成邀请失败。
	already, err := h.store.IsGroupMember(msg.Topic, msg.User)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeInternal, "membership check failed"))
		return
	}
	if already {
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionInvite, Code: CodeOK, Topic: msg.Topic, User: msg.User})
		return
	}
	// M14: see handleCreateGrp's identical gate for the full rationale — this
	// checked that the CALLER is a group member, but nothing verified any
	// relationship between the caller and msg.User being added.
	inviteeAppUserID, err := h.resolveAppUserID(msg.User)
	if err != nil {
		logf("resolveAppUserID(%s) failed: %v", msg.User, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "cannot verify relationship"))
		return
	}
	friends, err := h.internalAPI.AreFriends(client.appUserID, inviteeAppUserID)
	if err != nil {
		logf("AreFriends check failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeInternal, "relationship check failed"))
		return
	}
	if !friends {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeAuth, "not friends"))
		return
	}
	// 一次 GetGroupInfo 同时供人数上限判断与下面的群名使用（改前是在通知分支里
	// 又单独查了一次）。
	info, err := h.store.GetGroupInfo(msg.Topic)
	if err != nil || info == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeNotFound, "group not found"))
		return
	}
	if info.Members+1 > CurrentRuntimeConfig().MaxMembers {
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeConflict, "群成员数量已达上限"))
		return
	}
	if err := h.store.AddGroupMember(msg.Topic, msg.User); err != nil {
		logf("Invite failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionInvite, CodeInternal, "invite failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionInvite, Code: CodeOK, Topic: msg.Topic, User: msg.User})
	logf("[Group] %s invited %s to %s", client.uid, msg.User, msg.Topic)

	// Notify the invited user immediately if they're online.
	if target := h.GetClientByUID(msg.User); target != nil {
		// Same gap as handleCreateGrp: AddGroupMember only wrote the DB row.
		// The invitee stayed absent from h.topics (no BroadcastToTopic
		// delivery) yet present in h.clients (so pushOfflineRecipients skipped
		// them too), and the client's own handleGroupSync adds the topic to
		// knownTopics WITHOUT subscribing — which then suppresses every
		// client-side repair path until a full re-login.
		target.Subscribe(msg.Topic)
		target.SendJSON(&ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, Name: info.Name, From: client.uid})
	} else {
		// F-05：被邀请人不在线时改前什么都不留，他要等到下一次 get_groups 才
		// 可能发现自己进了群。与 handleCreateGrp 是同一个缺口。
		h.enqueueGroupEventForOffline(msg.Topic, []string{msg.User}, client.uid, ActionGrpSync, info.Name)
	}
}

func (h *Hub) handleKick(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeBadReq, "topic and user required"))
		return
	}
	// F-19(a)：踢自己。RemoveGroupMember 不看角色，群主踢掉自己就留下一个
	// owner_uid 指向不存在成员行的群 —— 没人能再改名/转让/解散，也没人能被
	// 提为管理员。退群应该走 leave_grp（那里有「群主必须先转让或解散」的判据）。
	if msg.User == client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeBadReq, "cannot kick yourself"))
		return
	}
	// 群主或管理员都可以踢人（改前只有群主）。
	if !h.ensureGroupManager(client, msg, ActionKick, msg.Topic) {
		return
	}
	// F-19(b)(c)：改前不看目标角色。管理员上线后，「管理员互踢」和「管理员踢
	// 群主」都会成立 —— 后者尤其致命，等于任何一个管理员都能把群主踢出他自己
	// 的群，之后这个群永远没有群主。
	targetRole, err := h.store.GetMemberRole(msg.Topic, msg.User)
	if err != nil {
		logf("[kick] GetMemberRole failed topic=%s uid=%s: %v", msg.Topic, msg.User, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeInternal, "member lookup failed"))
		return
	}
	if targetRole == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeBadReq, "target is not a group member"))
		return
	}
	if targetRole == RoleOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeAuth, "cannot remove the group owner"))
		return
	}
	if targetRole == RoleAdmin {
		// 管理员之间平级，互相踢不了；只有群主能撤掉一位管理员。
		callerIsOwner, ownerErr := h.store.IsGroupOwner(msg.Topic, client.uid)
		if ownerErr != nil {
			logf("[kick] IsGroupOwner failed topic=%s uid=%s: %v", msg.Topic, client.uid, ownerErr)
			client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeInternal, "member lookup failed"))
			return
		}
		if !callerIsOwner {
			client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeAuth, "only group owner can remove an administrator"))
			return
		}
	}
	if err := h.store.RemoveGroupMember(msg.Topic, msg.User); err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionKick, CodeInternal, "kick failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionKick, Code: CodeOK, Topic: msg.Topic, User: msg.User})
	logf("[Group] %s kicked %s from %s", client.uid, msg.User, msg.Topic)

	// RemoveGroupMember only clears the DB row — without this, a kicked user
	// who's still connected keeps their in-memory topic subscription and
	// keeps receiving live group messages until their next reconnect. Reuse
	// grp_disband: from the kicked user's point of view "I'm no longer in
	// this group" has the same client-side effect (drop it from the
	// conversation list) as the group being disbanded.
	if kicked := h.GetClientByUID(msg.User); kicked != nil {
		kicked.Unsubscribe(msg.Topic)
		kicked.SendJSON(&ServerMessage{Action: ActionGrpDisband, Topic: msg.Topic, From: client.uid})
	} else {
		// F-07：被踢的人不在线时改前什么都不留。他的成员行已经没了，
		// GetTopicMemberUIDs 从此不再枚举他，所以也不会有任何后续消息或推送
		// 顺带纠正他本地那条会话 —— 那个群会一直挂在他的会话列表里，点进去
		// 是空的、发消息 401。
		h.enqueueGroupEventForOffline(msg.Topic, []string{msg.User}, client.uid, ActionGrpDisband, "")
	}
	// Nudge remaining members so an open group-info/member-list screen
	// refreshes immediately instead of showing the kicked user until reload.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, "")

	// A Sender Key is a shared symmetric chain every current member holds —
	// removing a member from an encrypted group does nothing on its own to
	// stop them decrypting messages sent with a chain they already have.
	// Reusing ActionE2eSetGroupEncrypted as a "your sender key material is
	// stale, re-derive and re-distribute a fresh one" signal (rather than a
	// new action) keeps this idempotent from the client's point of view: it
	// is the same "group's encryption state just advanced" event whether
	// the reason was the initial owner toggle or a subsequent membership
	// change, and a client that ignores the distinction (e.g. re-runs
	// GroupSessionBuilder.create + re-distributes unconditionally on either)
	// behaves correctly either way. Only remaining members need this — the
	// kicked user already got ActionGrpDisband above and no longer has (or
	// should keep) this group's key material at all.
	if encrypted, encErr := h.store.IsGroupEncrypted(msg.Topic); encErr == nil && encrypted {
		h.BroadcastToTopic(msg.Topic, &ServerMessage{
			Action: ActionE2eSetGroupEncrypted, Topic: msg.Topic, From: client.uid,
			E2eGroupEncrypted: true, Reason: "member_removed",
		}, "")
	}
}

func (h *Hub) handleLeaveGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeBadReq, "topic required"))
		return
	}
	// Owners can't just walk away and leave the group ownerless — they must
	// transfer ownership or disband first, unless they're the last member
	// left, in which case leaving is equivalent to disbanding.
	// F-20：改前是 err == nil && isOwner。查询一失败就当「不是群主」，直接落到
	// 下面的 RemoveGroupMember —— 群主被当成普通成员移除，群从此无主。与紧邻的
	// GetGroupInfo 失败分支同一个判据：确认不了状态就报错让客户端重试，绝不猜。
	isOwner, ownerErr := h.store.IsGroupOwner(msg.Topic, client.uid)
	if ownerErr != nil {
		logf("[leave_grp] IsGroupOwner failed topic=%s uid=%s: %v", msg.Topic, client.uid, ownerErr)
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "无法确认群组状态，请重试"))
		return
	}
	if isOwner {
		info, infoErr := h.store.GetGroupInfo(msg.Topic)
		// H8: GetGroupInfo failing is NOT the same fact as "this group has
		// one member left" — the old code only guarded the "still has other
		// members" branch on infoErr == nil, so any transient error here
		// (DB hiccup, connection blip) fell straight through to the
		// disband call below and silently destroyed a group that could
		// have had any number of other members in it. Report the failure
		// and let the client retry instead of guessing.
		if infoErr != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "无法确认群组状态，请重试"))
			return
		}
		if info != nil && info.Members > 1 {
			client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeConflict, "群主需先转让群主身份或解散群聊，才能退出群聊"))
			return
		}
		// Confirmed last member — leaving really is a disband. Broadcast
		// before removing membership rows (same ordering as handleDisbandGrp,
		// and for the same reason: BroadcastToTopic only reaches clients
		// whose in-memory subscription is still intact). In the ordinary
		// case there's nobody else subscribed to hear it, but this closes
		// the gap for the case that motivated the infoErr check above: if
		// membership accounting is ever off by one, remaining members now
		// get told the group is gone instead of silently losing it.
		h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpDisband, Topic: msg.Topic, From: client.uid}, client.uid)
		if err := h.store.DisbandGroup(msg.Topic); err != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "leave failed"))
			return
		}
		client.Unsubscribe(msg.Topic)
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionLeaveGrp, Code: CodeOK, Topic: msg.Topic})
		return
	}
	if err := h.store.RemoveGroupMember(msg.Topic, client.uid); err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionLeaveGrp, CodeInternal, "leave failed"))
		return
	}
	client.Unsubscribe(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionLeaveGrp, Code: CodeOK, Topic: msg.Topic})
	// H9: an ordinary member leaving used to notify nobody. Remaining
	// members' in-memory subscription (h.topics[topic]) still includes
	// this client at the moment RemoveGroupMember's DB row disappears, so
	// unlike the owner-leaves-last case above there ARE other people who
	// need to hear about it — mirrors handleKick's identical nudge.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, client.uid)
	// Same sender-key-rotation signal as handleKick — a voluntary leave
	// removes a Sender Key holder exactly the same way a kick does. See the
	// comment there for why this reuses ActionE2eSetGroupEncrypted.
	if encrypted, encErr := h.store.IsGroupEncrypted(msg.Topic); encErr == nil && encrypted {
		h.BroadcastToTopic(msg.Topic, &ServerMessage{
			Action: ActionE2eSetGroupEncrypted, Topic: msg.Topic, From: client.uid,
			E2eGroupEncrypted: true, Reason: "member_left",
		}, client.uid)
	}
}

func (h *Hub) handleTransferOwner(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeBadReq, "topic and user required"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeNotFound, "group not found"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeAuth, "only group owner can transfer ownership"))
		return
	}
	if isMember, err := h.store.IsGroupMember(msg.Topic, msg.User); err != nil || !isMember {
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeBadReq, "target is not a group member"))
		return
	}
	if err := h.store.TransferGroupOwnership(msg.Topic, client.uid, msg.User); err != nil {
		logf("TransferGroupOwnership failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionTransferOwner, CodeInternal, "transfer failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionTransferOwner, Code: CodeOK, Topic: msg.Topic, User: msg.User})
	logf("[Group] %s transferred ownership of %s to %s", client.uid, msg.Topic, msg.User)

	// Nudge every currently-connected member (not just the new owner) so any
	// open group-info screen refreshes its role badges immediately.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, "")
}

// setMemberRoleAck 是 set_member_role 的成功回执。
//
// 刻意不复用 ServerMessage：协议契约要求这条回执带 memberRole 字段，而
// ServerMessage 目前没有这个字段，protocol.go 又不归本文件改。用一个只服务于
// 这一条回执的私有结构体，产出的 JSON 与契约逐字一致（id/action/code/topic/
// user/memberRole），端侧按 (action, id) 关联的方式完全不受影响。失败路径仍
// 走 ErrorResponse（那些帧本来就不需要 memberRole）。
type setMemberRoleAck struct {
	Id         string `json:"id,omitempty"`
	Action     string `json:"action"`
	Code       int    `json:"code"`
	Topic      string `json:"topic"`
	User       string `json:"user"`
	MemberRole string `json:"memberRole"`
}

// handleSetMemberRole 授予/收回管理员，**仅群主**可用（决策 D1 只放宽了清空
// 群记录，任命管理员本身仍是群主专属：管理员能自己造管理员的话，一次误操作就
// 会不可收敛地扩散下去）。
//
// RoleOwner 永远不可由此赋予：群主身份只能经 transfer_owner 移动，那条路径会
// 在同一个事务里把旧群主降级，保证任何时刻只有一行 owner。
func (h *Hub) handleSetMemberRole(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.User == "" || msg.MemberRole == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "topic, user and memberRole required"))
		return
	}
	if msg.MemberRole != RoleAdmin && msg.MemberRole != RoleMember {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "invalid member role"))
		return
	}
	// 角色只对群聊有意义。1:1 会话的 im_topic_members 也有 role 列（都是
	// 'member'），不挡住的话就能往一条私聊里写出一个「管理员」，而下游
	// CanManageGroup 只看 role —— 那个人立刻拿到对这条私聊的清空权限。
	isGroup, err := h.store.IsGroupTopic(msg.Topic)
	if err != nil {
		logf("[set_member_role] IsGroupTopic failed topic=%s: %v", msg.Topic, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeNotFound, "group not found"))
		return
	}
	if !isGroup {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "only group topics support member roles"))
		return
	}
	// 群主改自己 = 自我降级，会留下一个 owner_uid 指向 role='member' 那行的群，
	// 两个真相源从此打架。要放权就用 transfer_owner。
	if msg.User == client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "cannot change your own role"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		logf("[set_member_role] IsGroupOwner failed topic=%s uid=%s: %v", msg.Topic, client.uid, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeNotFound, "group not found"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeAuth, "only group owner can set member roles"))
		return
	}
	targetRole, err := h.store.GetMemberRole(msg.Topic, msg.User)
	if err != nil {
		logf("[set_member_role] GetMemberRole failed topic=%s uid=%s: %v", msg.Topic, msg.User, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeInternal, "member lookup failed"))
		return
	}
	if targetRole == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeBadReq, "target is not a group member"))
		return
	}
	if targetRole == msg.MemberRole {
		// 幂等短路：已经是这个角色，不写库也不再广播一次 grp_sync（重复广播会让
		// 每个在场客户端白刷一次成员名册）。
		client.SendAny(&setMemberRoleAck{
			Id: msg.Id, Action: ActionSetMemberRole, Code: CodeOK,
			Topic: msg.Topic, User: msg.User, MemberRole: msg.MemberRole,
		})
		return
	}
	if msg.MemberRole == RoleAdmin {
		// 管理员数量上限。没有它，群主可以把整群都设成管理员，而管理员现在拥有
		// 「清空全群聊天记录」这种不可逆权限。
		admins, err := h.store.CountGroupAdmins(msg.Topic)
		if err != nil {
			logf("[set_member_role] CountGroupAdmins failed topic=%s: %v", msg.Topic, err)
			client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeInternal, "member lookup failed"))
			return
		}
		if admins >= CurrentRuntimeConfig().MaxAdmins {
			client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeConflict, "admin limit reached"))
			return
		}
	}
	if err := h.store.SetGroupMemberRole(msg.Topic, msg.User, msg.MemberRole); err != nil {
		logf("[set_member_role] SetGroupMemberRole failed topic=%s uid=%s role=%s: %v", msg.Topic, msg.User, msg.MemberRole, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionSetMemberRole, CodeInternal, "set member role failed"))
		return
	}
	client.SendAny(&setMemberRoleAck{
		Id: msg.Id, Action: ActionSetMemberRole, Code: CodeOK,
		Topic: msg.Topic, User: msg.User, MemberRole: msg.MemberRole,
	})
	logf("[Group] %s set %s role=%s in %s", client.uid, msg.User, msg.MemberRole, msg.Topic)

	// 广播给**所有**在场成员（含被改的人自己和发起者）：群信息页的角色徽章、
	// 以及「管理群聊」入口是否可见，都由这份名册决定。与 transfer_owner 的
	// 收尾一致，excludeUID 传空串。
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, From: client.uid}, "")
}

func (h *Hub) handleDisbandGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeBadReq, "topic required"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeNotFound, "group not found"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeAuth, "only group owner can disband"))
		return
	}
	// F-07：先取成员名单。DisbandGroup 会把 im_topic_members 整组清掉，之后
	// 再也无从知道「解散那一刻谁在群里」，离线成员的补投递就没法入队了。
	members, memberErr := h.store.GetTopicMemberUIDs(msg.Topic)
	if memberErr != nil {
		// 不阻断解散本身：拿不到名单只是离线成员少一次主动通知，他们下次
		// get_groups 仍会自愈。
		logf("handleDisbandGrp: member lookup failed for %s: %v", msg.Topic, memberErr)
	}
	// Broadcast before removing membership rows — BroadcastToTopic only
	// reaches clients whose in-memory subscription is still intact.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpDisband, Topic: msg.Topic, From: client.uid}, "")
	if err := h.store.DisbandGroup(msg.Topic); err != nil {
		logf("DisbandGroup failed: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionDisbandGrp, CodeInternal, "disband failed"))
		return
	}
	// F-21：群没了，两份以 topic 为 key 的进程内缓存必须一起失效。AI 问答设置
	// 缓存 TTL 是 60s：不清的话这个 topic 万一被后续逻辑再次触及，读到的还是
	// 已解散那个群的知识库绑定。群名缓存同理。
	h.invalidateGroupAiSettingsCache(msg.Topic)
	h.invalidateGroupNameCache(msg.Topic)
	client.Unsubscribe(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionDisbandGrp, Code: CodeOK, Topic: msg.Topic})
	logf("[Group] %s disbanded %s", client.uid, msg.Topic)
	h.enqueueGroupEventForOffline(msg.Topic, members, client.uid, ActionGrpDisband, "")
}

func (h *Hub) handleUpdateGrp(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.Name == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeBadReq, "topic and name required"))
		return
	}
	// 群主或管理员都可以改群名（改前只有群主）。
	if !h.ensureGroupManager(client, msg, ActionUpdateGrp, msg.Topic) {
		return
	}
	// 群名长度改前完全没有校验：一条 update_grp 就能把任意长度的串写进
	// im_topics.name，而这个值会跟着每条群消息的 data 帧和每条离线 FCM 推送
	// 一起发出去。按 rune 计而不是 len()：中文群名在 UTF-8 下每字 3 字节。
	if utf8.RuneCountInString(msg.Name) > CurrentRuntimeConfig().NameMaxLen {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeBadReq, "群名称过长"))
		return
	}
	if err := h.store.UpdateGroupName(msg.Topic, msg.Name); err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGrp, CodeInternal, "rename failed"))
		return
	}
	h.invalidateGroupNameCache(msg.Topic)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionUpdateGrp, Code: CodeOK, Topic: msg.Topic, Name: msg.Name})
	// F-37：改前只有发起改名的那个人拿到了新群名，其余成员的会话列表要等到
	// 下一次 get_groups 才更新 —— 群名在他们眼里可以过期几个小时。排除发起者：
	// 他刚拿到的那条 200 ack 里已经带着新名字了。
	h.BroadcastToTopic(msg.Topic, &ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, Name: msg.Name, From: client.uid}, client.uid)
}

func (h *Hub) handleGetGrps(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGrps, CodeAuth, "not authenticated"))
		return
	}
	groups, err := h.store.GetUserGroups(client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGrps, CodeInternal, "retrieve failed"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGetGrps, Code: CodeOK, Groups: groups})
}

func (h *Hub) handleGetMbrs(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetMbrs, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetMbrs, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureTopicMember(client, msg, ActionGetMbrs, msg.Topic) {
		return
	}
	members, err := h.store.GetGroupMembers(msg.Topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetMbrs, CodeNotFound, "group not found"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGetMbrs, Code: CodeOK, Topic: msg.Topic, Members: members})
}

func (h *Hub) handleRegToken(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Token == "" {
		return
	}
	if err := h.store.SaveFCMToken(client.uid, msg.Token); err != nil {
		logf("SaveFCMToken failed: %v", err)
	}
}

// handleUnregToken is the logout-time counterpart to handleRegToken. Fire-
// and-forget like reg_token: there is nothing meaningful to tell the client
// if the delete fails (they're already logging out), and no ack is expected.
// Scoped by (uid, token) via DeleteFCMToken rather than deleting the token
// outright, so a logout racing a fresh login on a different account with the
// same device (SaveFCMToken already having reassigned the row) can't delete
// the new owner's row instead of the one actually being logged out of.
func (h *Hub) handleUnregToken(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Token == "" {
		return
	}
	if err := h.store.DeleteFCMToken(client.uid, msg.Token); err != nil {
		logf("DeleteFCMToken failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Batch forward (forward_bundle / forward_broadcast)
// ---------------------------------------------------------------------------

const (
	// forwardDepthLimit rejects re-forwarding a message that's already been
	// forwarded this many times, so a chain of forwards can't recurse
	// indefinitely (plan §2.2(1)/§9.3 medium-4).
	forwardDepthLimit = 3
)

// handleForwardBundle copies an ordered list of messages from one topic into
// a single target topic inside one DB transaction: either every message
// lands (in the caller's requested order) or none do. This is the "多条消息
// →一个群" mode — see plan §3.2 for why it needs all-or-nothing semantics
// where forward_broadcast (below) deliberately does not.
func (h *Hub) handleForwardBundle(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		// Must go through sendForwardBundleResult (carries msg.ReqID), not
		// client.SendJSON(ErrorResponse(msg.Id, ...)): ErrorResponse produces a plain
		// ServerMessage with no "reqId" field, which the Android client's
		// sendReqIdRequest() has no way to correlate back to its pending
		// CompletableDeferred — the caller would silently hang until its own
		// timeout instead of getting an immediate answer. Every other
		// early-return in this function already gets this right; this one
		// didn't (see plan §9.2.3 follow-up).
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "not_authenticated")
		return
	}
	if msg.SourceTopic == "" || msg.TargetTopic == "" || len(msg.SourceMsgIDs) == 0 {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "bad_request")
		return
	}
	// 合并转发的条数上限改为运行期可配（group_chat.forward_bundle_max），
	// 不再是编译期常量。
	if len(msg.SourceMsgIDs) > CurrentRuntimeConfig().ForwardBundleMax {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "too_many_messages")
		return
	}

	// Caller must be able to read the source topic and publish into the
	// target — both are group topics for this feature, so IsGroupMember
	// covers both checks (plan §5.1).
	if isMember, err := h.store.IsGroupMember(msg.SourceTopic, client.uid); err != nil || !isMember {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "not_member")
		return
	}
	if isMember, err := h.store.IsGroupMember(msg.TargetTopic, client.uid); err != nil || !isMember {
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "not_member")
		return
	}

	// Fetch every source message up front, in the caller's requested order,
	// and reject the whole batch before touching the DB if any one is
	// missing or already too deep in the forward chain — "all or nothing" is
	// the entire point of bundle mode.
	originals := make([]*ForwardableMessage, 0, len(msg.SourceMsgIDs))
	for _, seq := range msg.SourceMsgIDs {
		m, err := h.store.GetMessageForForward(msg.SourceTopic, seq)
		if err != nil {
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "source_not_found")
			return
		}
		if m.ForwardChainDepth >= forwardDepthLimit {
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "forward_chain_too_deep")
			return
		}
		originals = append(originals, m)
	}

	tx, err := h.store.BeginTx()
	if err != nil {
		logf("handleForwardBundle: begin tx failed: %v", err)
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
		return
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	type pendingAnnounce struct {
		seq  int
		orig *ForwardableMessage
	}
	pending := make([]pendingAnnounce, 0, len(originals))
	insertedSeqs := make([]int, 0, len(originals))

	for _, orig := range originals {
		newSeq, err := h.store.GetNextSeqTx(tx, msg.TargetTopic)
		if err != nil {
			logf("handleForwardBundle: assign seq failed: %v", err)
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
			return
		}
		srcSeq := orig.Seq
		srcTopic := msg.SourceTopic
		if err := h.store.SaveMessageFullTx(tx, msg.TargetTopic, newSeq, client.uid, SenderKindUser,
			orig.Content, orig.ContentType, orig.FileURL, orig.FileName, orig.Extra, orig.Duration,
			&srcSeq, &srcTopic, orig.ForwardChainDepth+1); err != nil {
			logf("handleForwardBundle: insert failed: %v", err)
			h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
			return
		}
		insertedSeqs = append(insertedSeqs, newSeq)
		pending = append(pending, pendingAnnounce{seq: newSeq, orig: orig})
	}

	if err := tx.Commit(); err != nil {
		logf("handleForwardBundle: commit failed: %v", err)
		h.sendForwardBundleResult(client, msg.ReqID, false, nil, "internal_error")
		return
	}
	committed = true

	h.sendForwardBundleResult(client, msg.ReqID, true, insertedSeqs, "")

	// Broadcast/push only after COMMIT — announcing a row that later rolled
	// back would show recipients a message the DB never actually kept.
	for _, p := range pending {
		srcSeq := p.orig.Seq
		srcTopic := msg.SourceTopic
		h.announceMessage(msg.TargetTopic, p.seq, client.uid, SenderKindUser,
			p.orig.Content, p.orig.ContentType, p.orig.FileURL, p.orig.FileName, p.orig.Extra, p.orig.Duration,
			&srcSeq, &srcTopic, "")
	}
	logf("[Forward] %s bundled %d message(s) from %s into %s", client.uid, len(insertedSeqs), msg.SourceTopic, msg.TargetTopic)
}

func (h *Hub) sendForwardBundleResult(client *Client, reqID string, ok bool, insertedSeqs []int, reason string) {
	client.SendAny(&ForwardBundleResult{
		Type: TypeForwardBundleResult, ReqId: reqID, Ok: ok,
		InsertedSeqs: insertedSeqs, Reason: reason,
	})
}

// forwardReqCacheTTL is how long handleForwardBroadcast remembers a reqId's
// outcome, so a client that reconnects mid-flight and replays the same
// request gets back the original results instead of forwarding the message
// into every target group a second time (plan §8.1 — "本方案中唯一需要引入幂等
// 机制的地方").
const forwardReqCacheTTL = 5 * time.Minute

type cachedForwardResult struct {
	results   []ForwardTargetResult
	expiresAt time.Time
}

// inFlightForwardMarker occupies forwardReqCache for a reqID for the
// duration of one handleForwardBroadcast call. Without it, the cache was
// pure check-then-act: a concurrent replay of the same reqID (the exact
// scenario this cache exists to guard against — see forwardReqCacheTTL's
// doc comment) would Load a miss while the original call was still mid-loop
// (it only Stores its result at the very end), race past the check, and
// forward the message into every target topic a second time. Claiming the
// slot up front via LoadOrStore closes that window: a second caller for the
// same reqID sees the marker (not a miss) and is rejected instead of
// re-executing the forward.
var inFlightForwardMarker = &struct{}{}

type forwardBroadcastClaim int

const (
	// forwardClaimOwn means no entry existed for this reqID (or the only
	// entry present was a stale, already-expired result); the caller now
	// owns the slot and must call finishForwardBroadcast when done so the
	// marker doesn't block this reqID forever.
	forwardClaimOwn forwardBroadcastClaim = iota
	// forwardClaimCached means a completed, unexpired result was found;
	// use it verbatim, do not forward again.
	forwardClaimCached
	// forwardClaimInFlight means another goroutine is currently executing
	// this exact reqID right now; do not forward again.
	forwardClaimInFlight
)

// claimForwardBroadcast attempts to atomically claim reqID for execution.
// See forwardBroadcastClaim's cases for what the three outcomes mean and
// what the caller must do in each. Empty reqID always returns
// forwardClaimOwn (matching the pre-existing behavior of skipping dedup
// entirely when no reqID was supplied — see the empty-string guards this
// replaces in the old getCachedForwardBroadcastResult).
func (h *Hub) claimForwardBroadcast(reqID string) (forwardBroadcastClaim, []ForwardTargetResult) {
	if reqID == "" {
		return forwardClaimOwn, nil
	}
	for {
		actual, loaded := h.forwardReqCache.LoadOrStore(reqID, inFlightForwardMarker)
		if !loaded {
			return forwardClaimOwn, nil
		}
		if actual == inFlightForwardMarker {
			return forwardClaimInFlight, nil
		}
		entry := actual.(cachedForwardResult)
		if !time.Now().After(entry.expiresAt) {
			return forwardClaimCached, entry.results
		}
		// Completed-but-expired entry the periodic gc sweep hasn't cleaned
		// up yet (gcForwardReqCache runs every 1m while entries live 5m).
		// Reclaim the slot for a fresh attempt instead of replaying a stale
		// result, then loop and retry the LoadOrStore.
		h.forwardReqCache.CompareAndDelete(reqID, actual)
	}
}

// finishForwardBroadcast records reqID's outcome and — critically —
// overwrites whatever inFlightForwardMarker claimForwardBroadcast placed
// there, releasing the claim so a later legitimate reqID reuse (or a
// concurrent forwardClaimInFlight caller that already replied with a
// failure) isn't left permanently blocked against an in-flight marker that
// will never clear.
func (h *Hub) finishForwardBroadcast(reqID string, results []ForwardTargetResult) {
	if reqID == "" {
		return
	}
	h.forwardReqCache.Store(reqID, cachedForwardResult{results: results, expiresAt: time.Now().Add(forwardReqCacheTTL)})
}

// gcForwardReqCache periodically evicts expired forward_broadcast idempotency
// entries so a long-running process doesn't accumulate one map entry per
// forward request forever. Started once from main.go alongside hub.Run().
func (h *Hub) gcForwardReqCache() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		h.forwardReqCache.Range(func(k, v interface{}) bool {
			// In-flight markers aren't cachedForwardResult — leave them;
			// finishForwardBroadcast will replace the marker once that
			// request completes.
			entry, ok := v.(cachedForwardResult)
			if ok && now.After(entry.expiresAt) {
				h.forwardReqCache.Delete(k)
			}
			return true
		})
	}
}

// handleForwardBroadcast copies one message into each of several target
// topics independently: a failure in one target (already left the group,
// group disbanded, forward chain too deep) does not roll back the others.
// This is the "一条消息→多个群" mode — see plan §3.1 for why best-effort is the
// right semantics here, unlike forward_bundle's all-or-nothing.
func (h *Hub) handleForwardBroadcast(client *Client, msg *ClientMessage) {
	// fail is declared before the early validation checks below (not just
	// before the business-logic checks that originally used it) so that
	// *every* rejection path — including "not authenticated" and "missing
	// required fields" — replies through the typed, reqId-bearing
	// ForwardBroadcastResult. Replying via client.SendJSON(ErrorResponse(msg.Id, ...))
	// instead (as those two checks did before) produces a plain ServerMessage
	// with no "reqId" field, which the Android client's sendReqIdRequest()
	// cannot correlate back to its pending CompletableDeferred — the caller
	// would silently hang until its own timeout instead of getting an
	// immediate answer (see plan §9.2.3 follow-up).
	fail := func(reason string) {
		results := make([]ForwardTargetResult, 0, len(msg.TargetTopics))
		for _, t := range msg.TargetTopics {
			results = append(results, ForwardTargetResult{Topic: t, Ok: false, Reason: reason})
		}
		h.finishForwardBroadcast(msg.ReqID, results)
		client.SendAny(&ForwardBroadcastResult{Type: TypeForwardBroadcastResult, ReqId: msg.ReqID, Results: results})
	}

	if !client.IsAuthenticated() {
		fail("not_authenticated")
		return
	}
	if msg.SourceTopic == "" || msg.SourceMsgID <= 0 || len(msg.TargetTopics) == 0 {
		fail("bad_request")
		return
	}
	// 逐条转发的目标数改前**完全没有上限**：一次请求就能让服务端串行地往任意
	// 多个群各写一条消息、各自再触发一轮广播与离线推送。
	if len(msg.TargetTopics) > CurrentRuntimeConfig().ForwardBroadcastMax {
		fail("too_many_targets")
		return
	}

	switch claim, cached := h.claimForwardBroadcast(msg.ReqID); claim {
	case forwardClaimCached:
		client.SendAny(&ForwardBroadcastResult{Type: TypeForwardBroadcastResult, ReqId: msg.ReqID, Results: cached})
		return
	case forwardClaimInFlight:
		// Another goroutine — most likely this exact request replayed by a
		// client that reconnected mid-flight — is already forwarding this
		// message right now. Do not forward it a second time; fail this
		// attempt instead. A legitimate retry a moment later will either
		// hit forwardClaimCached (the original finished) or get a fresh
		// attempt once the entry expires.
		fail("duplicate_in_flight")
		return
	}
	// forwardClaimOwn: this call now owns reqID's slot in forwardReqCache
	// and must reach finishForwardBroadcast (directly or via fail()) on
	// every remaining exit path below, or the slot is left claimed forever.

	if isMember, err := h.store.IsGroupMember(msg.SourceTopic, client.uid); err != nil || !isMember {
		fail("not_member")
		return
	}
	original, err := h.store.GetMessageForForward(msg.SourceTopic, msg.SourceMsgID)
	if err != nil {
		fail("source_not_found")
		return
	}
	if original.ForwardChainDepth >= forwardDepthLimit {
		fail("forward_chain_too_deep")
		return
	}

	results := make([]ForwardTargetResult, 0, len(msg.TargetTopics))
	for _, targetTopic := range msg.TargetTopics {
		exists, err := h.store.TopicExists(targetTopic)
		if err != nil || !exists {
			results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: false, Reason: "topic_not_found"})
			continue
		}
		isMember, err := h.store.IsGroupMember(targetTopic, client.uid)
		if err != nil || !isMember {
			results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: false, Reason: "not_member"})
			continue
		}
		srcSeq := original.Seq
		srcTopic := msg.SourceTopic
		seq, _, _, err := h.deliverMessage(targetTopic, client.uid, SenderKindUser,
			original.Content, original.ContentType, original.FileURL, original.FileName,
			original.Extra, original.Duration,
			&ForwardMeta{FromMsgID: srcSeq, FromTopic: srcTopic, ChainDepth: original.ForwardChainDepth + 1}, "")
		if err != nil {
			logf("handleForwardBroadcast: deliver to %s failed: %v", targetTopic, err)
			results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: false, Reason: "internal_error"})
			continue
		}
		results = append(results, ForwardTargetResult{Topic: targetTopic, Ok: true, Seq: seq})
	}

	h.finishForwardBroadcast(msg.ReqID, results)
	client.SendAny(&ForwardBroadcastResult{Type: TypeForwardBroadcastResult, ReqId: msg.ReqID, Results: results})
	logf("[Forward] %s broadcast msg %d/%s to %d target(s)", client.uid, msg.SourceMsgID, msg.SourceTopic, len(msg.TargetTopics))
}

// ---------------------------------------------------------------------------
// Group AI QA settings (get_group_ai_settings / update_group_ai_settings)
// ---------------------------------------------------------------------------

// groupAiSettingsCacheTTL bounds how stale a cached read can be after a
// group's settings change through some path other than
// handleUpdateGroupAiSettings (which invalidates the cache immediately on
// write). Matches plan §4.4's 60s figure.
const groupAiSettingsCacheTTL = 60 * time.Second

type groupAiSettingsCacheEntry struct {
	settings  *GroupAiSettings
	expiresAt time.Time
}

// GetGroupAiSettingsCached is the read-through cache in front of
// Store.GetGroupAiSettings, used by dispatchAiQaTask so a burst of @小桃子
// mentions in the same group doesn't each round-trip to Postgres.
func (h *Hub) GetGroupAiSettingsCached(topic string) (*GroupAiSettings, error) {
	if v, ok := h.groupAiSettingsCache.Load(topic); ok {
		entry := v.(groupAiSettingsCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.settings, nil
		}
	}
	settings, err := h.store.GetGroupAiSettings(topic)
	if err != nil {
		return nil, err
	}
	h.groupAiSettingsCache.Store(topic, groupAiSettingsCacheEntry{settings: settings, expiresAt: time.Now().Add(groupAiSettingsCacheTTL)})
	return settings, nil
}

func (h *Hub) invalidateGroupAiSettingsCache(topic string) {
	h.groupAiSettingsCache.Delete(topic)
}

// groupNameCacheTTL 兜住「群名被本进程之外的路径改掉」这种情况（管理后台直接
// 改库等）。改名走 handleUpdateGrp 时会立即失效，不必等 TTL。
const groupNameCacheTTL = 60 * time.Second

type groupNameCacheEntry struct {
	name      string
	expiresAt time.Time
}

// groupNameCached 是 Store.GetGroupInfo 前面的读穿缓存，专供每条群消息都要跑
// 的两条路径：announceMessage（data 帧带群名，F-31）与 pushOfflineRecipients
// （离线推送标题）。GetGroupInfo 一次是两条 SQL（SELECT + COUNT），放在每条
// 群消息的必经之路上不缓存代价太大。
//
// 查不到就返回空串而不是报错：群名只是展示信息，拿不到最多是标题少一行，
// 绝不该因此中断一条消息的投递。
func (h *Hub) groupNameCached(topic string) string {
	if v, ok := h.groupNameCache.Load(topic); ok {
		entry := v.(groupNameCacheEntry)
		if time.Now().Before(entry.expiresAt) {
			return entry.name
		}
	}
	info, err := h.store.GetGroupInfo(topic)
	if err != nil || info == nil {
		return ""
	}
	h.groupNameCache.Store(topic, groupNameCacheEntry{name: info.Name, expiresAt: time.Now().Add(groupNameCacheTTL)})
	return info.Name
}

func (h *Hub) invalidateGroupNameCache(topic string) {
	h.groupNameCache.Delete(topic)
}

// sendGroupAiSettingsFallback replies to get_group_ai_settings with the same
// disabled/no-KB shape GetGroupAiSettings returns for a group that has never
// configured AI QA, tagged with the caller's reqId. Used for this handler's
// three reject/error paths instead of client.SendJSON(ErrorResponse(msg.Id, ...)):
// GroupAiSettingsMessage's field set is a locked cross-team contract (plan
// §9 "跨端协议契约") that intentionally has no ok/reason field to carry a
// distinct failure signal, and ErrorResponse's plain ServerMessage carries no
// "reqId" at all — the Android client's sendReqIdRequest() would have no way
// to correlate it back to the pending CompletableDeferred, so the caller
// would silently hang for the full timeout instead of getting an answer.
// Falling back to "AI QA looks disabled" fails closed (never answers on the
// group's behalf) rather than open, which is the safer default for the two
// paths that should be unreachable from a well-formed client (not
// authenticated / empty topic) as well as for the one that's a genuine
// backend hiccup (DB error).
func (h *Hub) sendGroupAiSettingsFallback(client *Client, reqID, topic string) {
	client.SendAny(&GroupAiSettingsMessage{
		Type: TypeGroupAiSettings, ReqId: reqID, Topic: topic,
		AiQaEnabled: false, AiKbIds: []string{}, AiDocIds: []string{},
		AiDailyQuota: 50, AiCooldownSec: 5,
	})
}

func (h *Hub) handleGetGroupAiSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	if msg.Topic == "" {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	// Prior to this fix there was no membership check at all: any authenticated
	// client supplying any group topic string -- including one for a group
	// they were kicked from or never joined -- could read that group's AI
	// Q&A config, including which knowledge base doc IDs it's bound to.
	// Checked inline rather than via ensureTopicMember (the shared helper
	// every other action uses) because this action replies over the
	// reqId-correlated channel (SendAny / sendGroupAiSettingsFallback), and
	// ensureTopicMember's ErrorResponse carries no reqId field -- reusing it
	// here would leave the Android client's sendReqIdRequest() waiting for a
	// response that can never be matched, stuck until its own timeout
	// instead of failing fast. Same reasoning as the comment on
	// handleUpdateGroupAiSettings's auth-failure branch just below.
	// Fail-closed on a DB error, same as ensureTopicMember does.
	if isMember, err := h.store.IsGroupMember(msg.Topic, client.uid); err != nil || !isMember {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	settings, err := h.GetGroupAiSettingsCached(msg.Topic)
	if err != nil {
		h.sendGroupAiSettingsFallback(client, msg.ReqID, msg.Topic)
		return
	}
	client.SendAny(&GroupAiSettingsMessage{
		Type: TypeGroupAiSettings, ReqId: msg.ReqID, Topic: msg.Topic,
		AiQaEnabled: settings.AiQaEnabled, AiKbIds: settings.AiKbIds, AiDocIds: settings.AiDocIds,
		AiDailyQuota: settings.AiDailyQuota, AiCooldownSec: settings.AiCooldownSec,
	})
}

// handleUpdateGroupAiSettings is owner-only (plan §5.1), gated by IsGroupOwner
// exactly as handleDisbandGrp is.
//
// This used to name handleKick as the second precedent. That is now wrong and
// misleading in the worst direction: handleKick's primary gate is
// ensureGroupManager (owner OR admin), and IsGroupOwner survives there only in
// the narrow sub-branch that stops one admin from removing another. Reading
// handleKick as an owner-only precedent would suggest kicking is still owner-
// exclusive, which is the opposite of what it does.
func (h *Hub) handleUpdateGroupAiSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		// Reply through the typed, reqId-bearing UpdateGroupAiSettingsResult
		// (same struct every other path below already uses), not
		// client.SendJSON(ErrorResponse(msg.Id, ...)): that produces a plain
		// ServerMessage with no "reqId" field, which the Android client's
		// sendReqIdRequest() cannot correlate back to its pending
		// CompletableDeferred — the caller would silently hang until its own
		// timeout instead of getting an immediate answer.
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "not_authenticated"})
		return
	}
	if msg.Topic == "" {
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "bad_request"})
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "topic_not_found"})
		return
	}
	if !isOwner {
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "not_owner"})
		return
	}
	kbIds := msg.AiKbIds
	if kbIds == nil {
		kbIds = []string{}
	}
	docIds := msg.AiDocIds
	if docIds == nil {
		docIds = []string{}
	}
	if err := h.store.UpsertGroupAiSettings(msg.Topic, msg.AiQaEnabled, kbIds, docIds, client.uid); err != nil {
		logf("handleUpdateGroupAiSettings: %v", err)
		client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: false, Reason: "internal_error"})
		return
	}
	// Invalidate immediately rather than waiting out the TTL — otherwise a
	// group owner who just disabled AI QA could still see it answer for up
	// to 60s (plan §9.3 low-7).
	h.invalidateGroupAiSettingsCache(msg.Topic)
	client.SendAny(&UpdateGroupAiSettingsResult{Type: TypeUpdateGroupAiSettingsResult, ReqId: msg.ReqID, Ok: true})
	logf("[GroupAiQA] %s updated settings for %s: enabled=%v kbIds=%d docIds=%d", client.uid, msg.Topic, msg.AiQaEnabled, len(kbIds), len(docIds))
}

// -- E2EE --
//
// This server never has (and structurally cannot have) the plaintext or key
// material for anything below — every handler here either relays opaque
// ciphertext bytes it cannot interpret, or reads/writes public key material
// that is safe to store server-side by definition (see im_e2e_identities'
// doc comment). The one thing enforced server-side is *authorization to
// exchange ciphertext at all* (friendship for 1:1, membership+encrypted-flag
// for groups) and *routing* (deliver live, or queue with a bounded TTL) —
// never content.

// e2ePeerAllowed reports whether client is allowed to establish/use an
// encrypted 1:1 thread with peerUID — the same "already share a p2p_ topic,
// or are friends" gate resolveP2PTopic applies to plain 1:1 messaging,
// without resolveP2PTopic's side effect of creating that plain topic (an
// encrypted thread must never leave that trace — see ActionE2eSend's
// protocol.go doc comment). Deliberately returns no response itself: unlike
// most gate helpers in this file, its two callers (handleE2eFetchBundle,
// handleE2eSend) reply through different wire shapes (a reqId-correlated
// E2eBundleMessage vs a plain id-correlated ServerMessage — see
// handleGetGroupAiSettings's doc comment above for why that distinction
// matters), so only the caller knows which shape to answer with.
func (h *Hub) e2ePeerAllowed(client *Client, peerUID string) (bool, error) {
	if isMember, err := h.store.IsGroupMember(h.store.GetP2PTopic(client.uid, peerUID), client.uid); err == nil && isMember {
		return true, nil
	}
	// Group Sender Keys requires every member to be able to pairwise-wrap a
	// SenderKeyDistributionMessage to every OTHER member before that
	// member can decrypt anything (see the Android E2eeManager doc comment
	// on distributionMessageIfNeeded) — that wrapping is a real 1:1 Signal
	// session, which needs this same peer-allowed gate to pass for
	// e2e_fetch_bundle/e2e_send between the two members. Group co-membership
	// (already vetted by the owner at group creation — see handleCreateGrp's
	// own AreFriends check on owner->each-member) is the trust boundary that
	// actually applies here, independent of whether the two members also
	// happen to be mutual 1:1 friends — this codebase's group creation flow
	// never requires that between two non-owner members. Scoped to
	// ENCRYPTED groups only: an unencrypted group has no legitimate reason
	// for two members to fetch each other's E2EE bundle.
	if common, err := h.store.AreCommonEncryptedGroupMembers(client.uid, peerUID); err == nil && common {
		return true, nil
	}
	peerAppUserID, err := h.resolveAppUserID(peerUID)
	if err != nil {
		return false, err
	}
	return h.internalAPI.AreFriends(client.appUserID, peerAppUserID)
}

// relayE2eEnvelope delivers one encrypted payload to toUID: directly over
// their live socket if they're online right now (the common case, and the
// one that never touches disk at all — true real-time relay), or queued in
// im_e2e_envelopes with a bounded TTL if they're not (see that table's doc
// comment). A queue-write failure is logged, not surfaced to the sender —
// their own e2e_send already got CodeOK for the parts of the send that did
// go through; one offline recipient's queue write failing is not a reason
// to tell the sender the whole send failed when it may have reached the
// other N-1 group members fine.
func (h *Hub) relayE2eEnvelope(topic, fromUID, toUID, kind, payload string, msgType int) {
	// TrySendJSON (not SendJSON) on purpose: a full send buffer here must NOT
	// silently drop the frame and force-close the connection the way an
	// ordinary push does — see TrySendJSON's own doc comment for why that
	// established recovery path (reconnect -> pullOfflineMessages) does not
	// apply to E2EE envelopes. Falling through to the same offline-queue
	// write used for a genuinely-offline recipient is strictly better: worst
	// case the connection also happens to be dead and about to reconnect
	// anyway, in which case deliverPendingE2eEnvelopes on that reconnect
	// picks this up exactly like any other queued envelope.
	if target := h.GetClientByUID(toUID); target != nil {
		delivered := target.TrySendJSON(&ServerMessage{
			Action: ActionE2eDeliver, Topic: topic, From: fromUID,
			E2eKind: kind, E2eMsgType: msgType, Content: payload,
			Timestamp: time.Now().UnixMilli(),
		})
		if delivered {
			return
		}
		logf("relayE2eEnvelope: send buffer full for to=%s, falling back to offline queue instead of dropping", toUID)
	}
	if err := h.store.EnqueueE2eEnvelope(topic, toUID, fromUID, kind, payload, msgType, CurrentE2eeConfig().e2eeEnvelopeTTL()); err != nil {
		logf("relayE2eEnvelope: enqueue failed topic=%s to=%s: %v", topic, toUID, err)
		return
	}
	// Wake the recipient up — queuing alone (above) means "the message will
	// be there next time they open the app", which used to be the entire
	// offline story: no system notification, no banner, nothing. Only for
	// kind=="msg": a "sender_key" envelope is key material, not something a
	// human should be told "you have a new message" over — pushing for it
	// would fire a notification with no corresponding new content to show
	// when they tap it.
	if kind == "msg" {
		go h.pushE2eFCM(toUID, topic)
	}
}

// pushE2eFCM wakes an offline recipient's device for a queued encrypted
// envelope. The payload is deliberately content-blind — no preview, no
// sender name, nothing derived from the ciphertext or from anything the
// server would have to have decrypted to know: the server structurally
// never holds E2EE plaintext (see relayE2eEnvelope's block comment / the
// "-- E2EE --" note above handleE2eSend), and an FCM payload is not an
// exception to that. The client shows a fixed, locale-generic "you have a
// new encrypted message" string regardless of what's in this payload — see
// FcmService.onMessageReceived's data["e2e"]=="true" branch.
func (h *Hub) pushE2eFCM(toUID, topic string) {
	rc := CurrentRuntimeConfig()
	if !rc.FCMEnabled || h.fcm == nil {
		return
	}
	tokensByUID, err := h.store.GetFCMTokensBatch([]string{toUID})
	if err != nil {
		logf("pushE2eFCM: GetFCMTokensBatch failed: %v", err)
		return
	}
	data := map[string]string{
		"type":    "e2e_message",
		"e2e":     "true",
		"topic":   topic,
		"isGroup": strconv.FormatBool(strings.HasPrefix(topic, "grp_")),
	}
	for _, token := range tokensByUID[toUID] {
		if err := sendFCMv1(h.fcm, token, data); err != nil {
			logf("pushE2eFCM: send failed for %s: %v", toUID, err)
			if errors.Is(err, ErrFCMTokenUnregistered) {
				if delErr := h.store.DeleteFCMTokenByValue(token); delErr != nil {
					logf("pushE2eFCM: DeleteFCMTokenByValue failed: %v", delErr)
				}
			}
		}
	}
}

// handleE2ePublishBundle stores/replaces the caller's X3DH+PQXDH prekey
// bundle. No authorization beyond "authenticated" — publishing your own
// public key material has no victim.
func (h *Hub) handleE2ePublishBundle(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendAny(&E2ePublishBundleResult{Type: TypeE2ePublishBundleResult, ReqId: msg.ReqID, Ok: false, Reason: "not_authenticated"})
		return
	}
	if !CurrentE2eeConfig().Enabled {
		client.SendAny(&E2ePublishBundleResult{Type: TypeE2ePublishBundleResult, ReqId: msg.ReqID, Ok: false, Reason: "e2ee_disabled"})
		return
	}
	b := msg.E2eBundle
	if b == nil || b.IdentityKey == "" || b.SignedPreKey == "" || b.SignedPreKeySignature == "" ||
		b.KyberPreKey == "" || b.KyberPreKeySignature == "" {
		client.SendAny(&E2ePublishBundleResult{Type: TypeE2ePublishBundleResult, ReqId: msg.ReqID, Ok: false, Reason: "bad_request"})
		return
	}
	if err := h.store.UpsertE2eIdentity(client.uid, 1, b); err != nil {
		logf("handleE2ePublishBundle: %v", err)
		client.SendAny(&E2ePublishBundleResult{Type: TypeE2ePublishBundleResult, ReqId: msg.ReqID, Ok: false, Reason: "internal_error"})
		return
	}
	client.SendAny(&E2ePublishBundleResult{Type: TypeE2ePublishBundleResult, ReqId: msg.ReqID, Ok: true})
	logf("[E2EE] %s published a bundle (%d one-time prekeys)", client.uid, len(b.OneTimePreKeys))
}

// handleE2eFetchBundle answers with a target account's current bundle plus
// (if the pool isn't exhausted) one freshly-consumed one-time prekey. Gated
// on the same friendship check as plain 1:1 messaging — public key material
// being safe to store doesn't mean it should be enumerable by strangers.
func (h *Hub) handleE2eFetchBundle(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.User == "" {
		client.SendAny(&E2eBundleMessage{Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: false})
		return
	}
	if !CurrentE2eeConfig().Enabled {
		client.SendAny(&E2eBundleMessage{Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: false})
		return
	}
	allowed, err := h.e2ePeerAllowed(client, msg.User)
	if err != nil {
		logf("handleE2eFetchBundle: e2ePeerAllowed(%s) failed: %v", msg.User, err)
		client.SendAny(&E2eBundleMessage{Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: false})
		return
	}
	if !allowed {
		client.SendAny(&E2eBundleMessage{Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: false})
		return
	}
	identity, err := h.store.GetE2eIdentity(msg.User)
	if err != nil {
		logf("handleE2eFetchBundle: GetE2eIdentity(%s) failed: %v", msg.User, err)
		client.SendAny(&E2eBundleMessage{Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: false})
		return
	}
	if identity == nil {
		client.SendAny(&E2eBundleMessage{Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: false})
		return
	}
	otpkID, otpk, hasOtpk, err := h.store.ConsumeOneE2eOneTimePreKey(msg.User)
	if err != nil {
		// A failed pool pop doesn't invalidate the rest of the bundle — X3DH
		// degrades gracefully to signed-prekey-only when no one-time key is
		// available. Logged, not fatal to this reply.
		logf("handleE2eFetchBundle: ConsumeOneE2eOneTimePreKey(%s) failed: %v", msg.User, err)
	}
	reply := &E2eBundleMessage{
		Type: TypeE2eBundle, ReqId: msg.ReqID, User: msg.User, Found: true,
		DeviceId: identity.DeviceID, RegistrationId: identity.RegistrationID,
		IdentityKey:    identity.IdentityKey,
		SignedPreKeyId: identity.SignedPreKeyID, SignedPreKey: identity.SignedPreKey,
		SignedPreKeySignature: identity.SignedPreKeySignature,
		KyberPreKeyId:         identity.KyberPreKeyID, KyberPreKey: identity.KyberPreKey,
		KyberPreKeySignature: identity.KyberPreKeySignature,
	}
	if hasOtpk {
		reply.OneTimePreKeyId = otpkID
		reply.OneTimePreKey = otpk
	}
	client.SendAny(reply)
}

// handleE2eOtpkCount answers "how many of my own unconsumed one-time
// prekeys are still on the server". No e2ePeerAllowed check — this is
// always about the caller's own account (see ActionE2eOtpkCount's doc
// comment), there is no peer to gate against.
func (h *Hub) handleE2eOtpkCount(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendAny(&E2eOtpkCountMessage{Type: TypeE2eOtpkCountResult, ReqId: msg.ReqID, Count: 0})
		return
	}
	n, err := h.store.CountUnconsumedE2eOneTimePreKeys(client.uid)
	if err != nil {
		logf("handleE2eOtpkCount: CountUnconsumedE2eOneTimePreKeys(%s) failed: %v", client.uid, err)
		client.SendAny(&E2eOtpkCountMessage{Type: TypeE2eOtpkCountResult, ReqId: msg.ReqID, Count: 0})
		return
	}
	client.SendAny(&E2eOtpkCountMessage{Type: TypeE2eOtpkCountResult, ReqId: msg.ReqID, Count: n})
}

// handleE2eSend relays one encrypted payload — a real message, a group
// Sender Key distribution, doesn't matter which, see ClientMessage.E2eKind —
// to every intended recipient. For a group (grp_ topic) the recipient list
// is always the server's own authoritative membership snapshot, never
// anything the client supplies, so there is no way for a client to widen
// delivery beyond the real roster. See the "-- E2EE --" block comment above
// for why this never touches im_messages.
func (h *Hub) handleE2eSend(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeAuth, "not authenticated"))
		return
	}
	if !CurrentE2eeConfig().Enabled {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeConflict, "e2ee_disabled"))
		return
	}
	if msg.Topic == "" || msg.E2eKind == "" || (msg.E2eKind == "msg" && msg.Content == "") {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeBadReq, "bad request"))
		return
	}

	var targets []string
	if strings.HasPrefix(msg.Topic, "grp_") {
		encrypted, err := h.store.IsGroupEncrypted(msg.Topic)
		if err != nil || !encrypted {
			client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeConflict, "group_not_encrypted"))
			return
		}
		if !h.ensureTopicMember(client, msg, ActionE2eSend, msg.Topic) {
			return
		}
		members, err := h.store.GetTopicMemberUIDs(msg.Topic)
		if err != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeInternal, "member lookup failed"))
			return
		}
		for _, m := range members {
			if m != client.uid {
				targets = append(targets, m)
			}
		}
	} else {
		if msg.User == "" {
			client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeBadReq, "user required"))
			return
		}
		allowed, err := h.e2ePeerAllowed(client, msg.User)
		if err != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeInternal, "friendship check failed"))
			return
		}
		if !allowed {
			client.SendJSON(ErrorResponse(msg.Id, ActionE2eSend, CodeAuth, "not friends"))
			return
		}
		targets = []string{msg.User}
	}

	for _, target := range targets {
		h.relayE2eEnvelope(msg.Topic, client.uid, target, msg.E2eKind, msg.Content, msg.E2eMsgType)
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionE2eSend, Code: CodeOK, Topic: msg.Topic})
}

// handleE2eTerminate relays "I deleted my side of this encrypted thread" to
// the peer, best-effort. See ActionE2eTerminate's protocol.go doc comment
// for why this is the entire "turn encryption off" mechanism, and why it's
// realtime-only with no offline queue.
func (h *Hub) handleE2eTerminate(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eTerminate, CodeBadReq, "bad request"))
		return
	}
	// No FRIENDSHIP re-check here on purpose: terminating is strictly weaker
	// than sending — if the two are no longer friends, telling the peer "the
	// encrypted thread you have with me is gone" cannot leak or damage
	// anything, and gating it would leave that peer stuck believing a thread
	// still exists that the sender has already deleted their side of. But
	// SOME relationship check is still required — with none at all, any two
	// authenticated accounts that have never interacted could spoof this
	// push at each other on demand, an unrated harassment/probing vector
	// (does target uid exist / is it online) with zero legitimate use.
	// Reusing e2ePeerAllowed rather than a friendship-only check preserves
	// the legitimate "we used to have a thread, I since unfriended them, I
	// still want to terminate my side" case: its p2p_ topic-membership
	// branch persists independent of current friendship status.
	if allowed, err := h.e2ePeerAllowed(client, msg.User); err != nil || !allowed {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eTerminate, CodeAuth, "not authorized"))
		return
	}
	if target := h.GetClientByUID(msg.User); target != nil {
		target.SendJSON(&ServerMessage{Action: ActionE2eTerminated, Topic: msg.Topic, From: client.uid})
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionE2eTerminate, Code: CodeOK, Topic: msg.Topic})
}

// handleE2eClear relays "please drop your local copy of this conversation's
// messages" to the peer, best-effort, WITHOUT touching the Double Ratchet
// session or the thread itself -- see ActionE2eClear's protocol.go doc
// comment for why this exists as a lighter sibling of handleE2eTerminate.
// Same authorization reasoning as handleE2eTerminate (reusing e2ePeerAllowed
// rather than a friendship-only check, no re-check needed since this is
// strictly weaker than sending), and the same realtime-only characteristic:
// the server holds nothing durable for E2EE content (see im_e2e_envelopes'
// doc comment), so there is nothing meaningful to queue for an offline peer
// here either -- if they're not connected right now, only the requester's
// own device ends up cleared, which callers must not assume is symmetric.
func (h *Hub) handleE2eClear(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" || msg.User == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eClear, CodeBadReq, "bad request"))
		return
	}
	if allowed, err := h.e2ePeerAllowed(client, msg.User); err != nil || !allowed {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eClear, CodeAuth, "not authorized"))
		return
	}
	if target := h.GetClientByUID(msg.User); target != nil {
		target.SendJSON(&ServerMessage{Action: ActionE2eCleared, Topic: msg.Topic, From: client.uid})
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionE2eClear, Code: CodeOK, Topic: msg.Topic})
}

// handleE2eGroupClear is the group counterpart to handleE2eClear -- clears an
// encrypted group's message history on every member's device without
// touching the group's Sender Key session state (not a "turn encryption
// off" action; that doesn't exist for groups at all, see
// ActionE2eSetGroupEncrypted's doc comment on why it's monotonic).
//
// Authorization is ensureGroupManager (owner+admin), the SAME tier plaintext
// group clear already uses in handleDelTopic's grp_ branch -- deliberately
// NOT the owner-only tier handleE2eSetGroupEncrypted uses. Reasoning: clearing
// is closer in kind to "tidy up the group's messages" than to "change the
// group's encryption posture", so it should sit at the same trust level as
// its plaintext sibling and reuse the same already-shipped authorization
// check, not invent a third tier.
//
// There is nothing to delete server-side -- encrypted group content never
// touches a persistent store (only the transient im_e2e_envelopes relay for
// offline recipients, same as 1:1). This handler is pure authorization +
// broadcast; every member's own client does its own local delete on
// receiving ActionE2eGroupCleared (or, for the caller, from its own
// already-in-flight local clear -- see excludeUID below).
func (h *Hub) handleE2eGroupClear(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() || msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionE2eGroupClear, CodeBadReq, "bad request"))
		return
	}
	if !h.ensureGroupManager(client, msg, ActionE2eGroupClear, msg.Topic) {
		return
	}
	// Caller's own device already clears locally as part of the same UI
	// action that triggered this request (see E2eeRepository.clearGroupHistory)
	// -- excluding it here avoids a redundant, harmless-but-confusing second
	// local clear racing the one already in flight.
	h.BroadcastToTopic(msg.Topic, &ServerMessage{
		Action: ActionE2eGroupCleared, Topic: msg.Topic, From: client.uid,
	}, client.uid)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionE2eGroupClear, Code: CodeOK, Topic: msg.Topic})
}

// handleE2eSetGroupEncrypted is owner-only (same tier as
// handleUpdateGroupAiSettings) and monotonic — see
// ActionE2eSetGroupEncrypted's protocol.go doc comment for why there is no
// "set it back to false" path at all. Unlike its AI-settings sibling this
// one does broadcast to the whole roster: every member's client needs to
// see the padlock and start participating in Sender Key setup from this
// moment, not just the owner's own other connections.
func (h *Hub) handleE2eSetGroupEncrypted(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: false, Reason: "not_authenticated"})
		return
	}
	if cfg := CurrentE2eeConfig(); !cfg.Enabled || !cfg.GroupEnabled {
		reason := "group_e2ee_disabled"
		if !cfg.Enabled {
			reason = "e2ee_disabled"
		}
		client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: false, Reason: reason})
		return
	}
	if msg.Topic == "" {
		client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: false, Reason: "bad_request"})
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: false, Reason: "topic_not_found"})
		return
	}
	if !isOwner {
		client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: false, Reason: "not_owner"})
		return
	}
	if err := h.store.SetGroupEncrypted(msg.Topic); err != nil {
		logf("handleE2eSetGroupEncrypted: %v", err)
		client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: false, Reason: "internal_error"})
		return
	}
	client.SendAny(&E2eSetGroupEncryptedResult{Type: TypeE2eSetGroupEncryptedResult, ReqId: msg.ReqID, Ok: true})
	h.BroadcastToTopic(msg.Topic, &ServerMessage{
		Action: ActionE2eSetGroupEncrypted, Topic: msg.Topic, From: client.uid,
		E2eGroupEncrypted: true, Reason: "owner_enabled",
	}, "")
	logf("[E2EE] %s enabled encryption for group %s", client.uid, msg.Topic)
}

// handleGroupQrToken mints-or-returns a group's current scan-to-join QR
// token. Viewing/sharing the current token is open to any member (mirrors
// WeChat: anyone in the group can bring someone in); RefreshQr=true
// additionally requires ensureGroupManager because invalidating the link
// everyone else may currently be relying on is a moderation-tier action,
// same tier as kick/rename, not a plain view.
func (h *Hub) handleGroupQrToken(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureTopicMember(client, msg, ActionGroupQrToken, msg.Topic) {
		return
	}
	if msg.RefreshQr && !h.ensureGroupManager(client, msg, ActionGroupQrToken, msg.Topic) {
		return
	}
	var tok *GroupInviteToken
	var err error
	if !msg.RefreshQr {
		tok, err = h.store.GetActiveGroupQrToken(msg.Topic)
		if err != nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeInternal, "获取二维码失败"))
			return
		}
	}
	if tok == nil {
		ttl := time.Duration(CurrentRuntimeConfig().QrTtlSeconds) * time.Second
		tok, err = h.store.MintGroupQrToken(msg.Topic, client.uid, ttl)
		if err != nil {
			logf("handleGroupQrToken: mint failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrToken, CodeInternal, "生成二维码失败"))
			return
		}
	}
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionGroupQrToken, Code: CodeOK, Topic: msg.Topic,
		QrToken: tok.Token, ExpiresAt: tok.ExpiresAt.UnixMilli(),
	})
}

// handleGroupQrResolve previews a scanned token's group before the caller
// decides to join/apply. Deliberately the one group_* action open to a
// non-member -- resolving before joining is the entire point of scanning a
// code you found on someone else's screen.
func (h *Hub) handleGroupQrResolve(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeAuth, "not authenticated"))
		return
	}
	if msg.QrToken == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeBadReq, "qrToken required"))
		return
	}
	topic, ok, err := h.store.ResolveGroupQrToken(msg.QrToken)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeInternal, "二维码校验失败"))
		return
	}
	if !ok {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeNotFound, "二维码不存在或已过期"))
		return
	}
	info, err := h.store.GetGroupInfo(topic)
	if err != nil || info == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeNotFound, "群聊不存在"))
		return
	}
	alreadyMember, err := h.store.IsGroupMember(topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeInternal, "校验失败"))
		return
	}
	settings, err := h.store.GetGroupJoinSettings(topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupQrResolve, CodeInternal, "校验失败"))
		return
	}
	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionGroupQrResolve, Code: CodeOK, Topic: topic, Name: info.Name,
		MemberCount: info.Members, RequireApproval: settings.RequireApproval,
		AlreadyMember: alreadyMember, IsFull: info.Members >= CurrentRuntimeConfig().MaxMembers,
	})
}

// handleGroupJoin is the single entry point for scan-to-join, whether or not
// the group requires approval -- the server re-resolves the token and
// re-decides based on the group's *current* settings rather than trusting
// whatever an earlier group_qr_resolve reply told the client, and answers
// via JoinStatus so the client never has to pre-guess which branch will run.
//
// Joining this way deliberately skips handleInvite's AreFriends check: a
// scan-to-join code has no existing member vouching for the newcomer by
// definition, which is the whole point of the feature (same relaxation
// WeChat's own group QR makes) -- but the MaxMembers cap still applies
// exactly as it does for invite.
func (h *Hub) handleGroupJoin(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeAuth, "not authenticated"))
		return
	}
	if msg.QrToken == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeBadReq, "qrToken required"))
		return
	}
	topic, ok, err := h.store.ResolveGroupQrToken(msg.QrToken)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "二维码校验失败"))
		return
	}
	if !ok {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeNotFound, "二维码不存在或已过期"))
		return
	}
	already, err := h.store.IsGroupMember(topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "校验失败"))
		return
	}
	if already {
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "joined"})
		return
	}
	info, err := h.store.GetGroupInfo(topic)
	if err != nil || info == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeNotFound, "群聊不存在"))
		return
	}
	if info.Members+1 > CurrentRuntimeConfig().MaxMembers {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeConflict, "群成员数量已达上限"))
		return
	}
	settings, err := h.store.GetGroupJoinSettings(topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "校验失败"))
		return
	}
	if settings.RequireApproval {
		message := msg.JoinMessage
		if runes := []rune(message); len(runes) > 60 {
			message = string(runes[:60])
		}
		if _, err := h.store.CreateGroupJoinRequest(topic, client.uid, message); err != nil {
			if errors.Is(err, ErrDuplicateJoinRequest) {
				client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "pending_approval"})
				return
			}
			logf("handleGroupJoin: create request failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "申请提交失败"))
			return
		}
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "pending_approval"})
		h.notifyGroupManagersOfJoinRequest(topic, client.uid, info.Name)
		return
	}
	if err := h.store.AddGroupMember(topic, client.uid); err != nil {
		logf("handleGroupJoin: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoin, CodeInternal, "加入失败"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoin, Code: CodeOK, Topic: topic, JoinStatus: "joined"})
	logf("[Group] %s joined %s via QR (no approval required)", client.uid, topic)
}

// handleGroupJoinRequestsList lists a group's pending scan-to-join requests
// for its owner/admins, resolving each requester's display name the same
// way pushOfflineRecipients resolves a sender's -- per-viewer, since the
// reviewer may have set a personal alias for the requester (see
// Contact.alias).
func (h *Hub) handleGroupJoinRequestsList(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinRequestsList, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinRequestsList, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureGroupManager(client, msg, ActionGroupJoinRequestsList, msg.Topic) {
		return
	}
	reqs, err := h.store.GetPendingGroupJoinRequests(msg.Topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinRequestsList, CodeInternal, "查询失败"))
		return
	}
	dtos := make([]GroupJoinRequestDto, 0, len(reqs))
	if len(reqs) == 0 {
		client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoinRequestsList, Code: CodeOK, Topic: msg.Topic, JoinRequests: dtos})
		return
	}
	viewerAppUserID, verr := h.store.GetAppUserID(client.uid)
	for _, r := range reqs {
		name := r.RequesterUID
		if verr == nil && viewerAppUserID != "" {
			if requesterAppUserID, aerr := h.store.GetAppUserID(r.RequesterUID); aerr == nil && requesterAppUserID != "" {
				if resolvedName, _, gerr := h.internalAPI.GetProfile(viewerAppUserID, requesterAppUserID); gerr == nil && resolvedName != "" {
					name = resolvedName
				}
			}
		}
		dtos = append(dtos, GroupJoinRequestDto{
			ID: r.ID, RequesterUID: r.RequesterUID, RequesterName: name,
			Message: r.Message, CreatedAt: r.CreatedAt.UnixMilli(),
		})
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoinRequestsList, Code: CodeOK, Topic: msg.Topic, JoinRequests: dtos})
}

// handleGroupJoinReview approves or rejects one pending request.
// ReviewGroupJoinRequest's WHERE status='pending' clause is the concurrency
// guard: if two admins review the same request at once, the loser gets nil
// back here and must be told "already handled", not silently re-apply their
// own verdict on top.
func (h *Hub) handleGroupJoinReview(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" || msg.JoinRequestID <= 0 {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeBadReq, "topic and joinRequestId required"))
		return
	}
	if !h.ensureGroupManager(client, msg, ActionGroupJoinReview, msg.Topic) {
		return
	}
	req, err := h.store.ReviewGroupJoinRequest(msg.JoinRequestID, msg.Approve, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeInternal, "处理失败"))
		return
	}
	if req == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeConflict, "该申请已被处理"))
		return
	}
	// ensureGroupManager only checked authority over msg.Topic, not over
	// whatever topic joinRequestId actually belongs to -- without this check
	// a manager of group A could review (and learn the existence of) a
	// request against group B just by guessing an id.
	if req.Topic != msg.Topic {
		client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeBadReq, "请求与群聊不匹配"))
		return
	}
	status := "rejected"
	if msg.Approve {
		status = "approved"
		info, err := h.store.GetGroupInfo(msg.Topic)
		if err != nil || info == nil {
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeNotFound, "群聊不存在"))
			return
		}
		if info.Members+1 > CurrentRuntimeConfig().MaxMembers {
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeConflict, "群成员数量已达上限"))
			return
		}
		if err := h.store.AddGroupMember(msg.Topic, req.RequesterUID); err != nil {
			logf("handleGroupJoinReview: add member failed: %v", err)
			client.SendJSON(ErrorResponse(msg.Id, ActionGroupJoinReview, CodeInternal, "处理失败"))
			return
		}
		if target := h.GetClientByUID(req.RequesterUID); target != nil {
			target.Subscribe(msg.Topic)
			target.SendJSON(&ServerMessage{Action: ActionGrpSync, Topic: msg.Topic, Name: info.Name, From: client.uid})
		} else {
			h.enqueueGroupEventForOffline(msg.Topic, []string{req.RequesterUID}, client.uid, ActionGrpSync, info.Name)
		}
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGroupJoinReview, Code: CodeOK, Topic: msg.Topic, JoinStatus: status})
	h.notifyRequesterOfReview(msg.Topic, req.RequesterUID, status)
}

func (h *Hub) handleGetGroupJoinSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGroupJoinSettings, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGroupJoinSettings, CodeBadReq, "topic required"))
		return
	}
	if !h.ensureTopicMember(client, msg, ActionGetGroupJoinSettings, msg.Topic) {
		return
	}
	settings, err := h.store.GetGroupJoinSettings(msg.Topic)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionGetGroupJoinSettings, CodeInternal, "查询失败"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionGetGroupJoinSettings, Code: CodeOK, Topic: msg.Topic, RequireApproval: settings.RequireApproval})
}

// handleUpdateGroupJoinSettings is owner-only, same precedent and same
// reasoning as handleUpdateGroupAiSettings: this toggle can leave a group
// without the protection it exists to provide if an admin could silently
// flip it off, which is the settings-toggle risk shape, not the
// moment-to-moment moderation shape of kick/rename (ensureGroupManager).
func (h *Hub) handleUpdateGroupJoinSettings(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeAuth, "not authenticated"))
		return
	}
	if msg.Topic == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeBadReq, "topic required"))
		return
	}
	isOwner, err := h.store.IsGroupOwner(msg.Topic, client.uid)
	if err != nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeNotFound, "群聊不存在"))
		return
	}
	if !isOwner {
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeAuth, "仅群主可以设置"))
		return
	}
	if err := h.store.UpsertGroupJoinSettings(msg.Topic, msg.RequireApproval, client.uid); err != nil {
		logf("handleUpdateGroupJoinSettings: %v", err)
		client.SendJSON(ErrorResponse(msg.Id, ActionUpdateGroupJoinSettings, CodeInternal, "设置失败"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionUpdateGroupJoinSettings, Code: CodeOK, Topic: msg.Topic, RequireApproval: msg.RequireApproval})
}

// notifyGroupManagersOfJoinRequest tells a group's owner and admins that a
// new scan-to-join request is awaiting review -- live push if connected,
// offline queue + best-effort FCM otherwise. Carries no request details
// itself (same "go refresh" shape as ActionGrpSync): a client always
// re-fetches via group_join_requests_list rather than trusting a snapshot
// embedded in the push.
func (h *Hub) notifyGroupManagersOfJoinRequest(topic, requesterUID, groupName string) {
	managers, err := h.store.GetGroupManagerUIDs(topic)
	if err != nil {
		logf("notifyGroupManagersOfJoinRequest: GetGroupManagerUIDs failed: %v", err)
		return
	}
	var offline []string
	for _, uid := range managers {
		if uid == requesterUID {
			continue
		}
		if target := h.GetClientByUID(uid); target != nil {
			target.SendJSON(&ServerMessage{Action: ActionGroupJoinRequestPush, Topic: topic, Name: groupName, From: requesterUID})
			continue
		}
		offline = append(offline, uid)
	}
	if len(offline) == 0 {
		return
	}
	h.enqueueGroupEventForOffline(topic, offline, requesterUID, PendingGroupEventJoinRequest, groupName)
	h.pushJoinFCM(offline, groupName, "有人申请加入「"+groupName+"」，点击查看详情并审核", topic)
}

// notifyRequesterOfReview tells an applicant whether their request was
// approved or rejected -- live push if online, offline queue + best-effort
// FCM otherwise.
func (h *Hub) notifyRequesterOfReview(topic, requesterUID, status string) {
	groupName := h.groupNameCached(topic)
	if target := h.GetClientByUID(requesterUID); target != nil {
		target.SendJSON(&ServerMessage{Action: ActionGroupJoinReviewed, Topic: topic, Name: groupName, JoinStatus: status})
		return
	}
	kind := PendingGroupEventJoinRejected
	body := "你申请加入「" + groupName + "」被拒绝"
	if status == "approved" {
		kind = PendingGroupEventJoinApproved
		body = "你的入群申请已通过，欢迎加入「" + groupName + "」"
	}
	if err := h.store.EnqueuePendingGroupEvent(topic, requesterUID, kind, groupName, ""); err != nil {
		logf("notifyRequesterOfReview: enqueue failed: %v", err)
	}
	h.pushJoinFCM([]string{requesterUID}, groupName, body, topic)
}

// pushJoinFCM is a lighter-weight sibling of pushOfflineRecipients for the
// small recipient sets these two join-flow notifications ever address (a
// group's manager roster is capped at CurrentRuntimeConfig().MaxAdmins+1;
// a review notice has exactly one recipient) -- no goroutine/semaphore pool
// needed at this scale, unlike a 500-member group's message fan-out.
func (h *Hub) pushJoinFCM(uids []string, groupName, body, topic string) {
	rc := CurrentRuntimeConfig()
	if !rc.FCMEnabled || h.fcm == nil {
		return
	}
	tokensByUID, err := h.store.GetFCMTokensBatch(uids)
	if err != nil {
		logf("pushJoinFCM: GetFCMTokensBatch failed: %v", err)
		return
	}
	data := map[string]string{
		"type":      "group_join",
		"topic":     topic,
		"groupName": groupName,
		"preview":   body,
		"isGroup":   "true",
	}
	for _, uid := range uids {
		for _, token := range tokensByUID[uid] {
			if err := sendFCMv1(h.fcm, token, data); err != nil {
				logf("pushJoinFCM: send failed for %s: %v", uid, err)
				if errors.Is(err, ErrFCMTokenUnregistered) {
					if delErr := h.store.DeleteFCMTokenByValue(token); delErr != nil {
						logf("pushJoinFCM: DeleteFCMTokenByValue failed: %v", delErr)
					}
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// @小桃子 mention detection + AI QA dispatch
// ---------------------------------------------------------------------------

// aiMentionRegex matches "@小桃子" followed by ASCII/CJK whitespace, common
// CJK/ASCII punctuation, another "@" (multi-mention with no separator), or
// end of string — not just \s, which would miss the two most common real
// inputs: "@小桃子，帮我查一下"（全角逗号续写）and "@小桃子@张三"（紧邻多重艾特）.
// It still correctly rejects "@小桃子的忠实粉丝群" (not in the boundary set).
// See plan §9.2.3.
var aiMentionRegex = regexp.MustCompile(`@小桃子(?:[\s\x{3000}，。！？：；、,.!?:;@]|$)`)

func hasValidAiMention(content string) bool {
	return aiMentionRegex.MatchString(content)
}

// aiCooldownMap tracks, per group topic, the last time an AI QA task was
// dispatched. tryAcquireAiCooldown uses LoadOrStore/CompareAndSwap instead of
// a Load-then-Store pair so two @小桃子 messages arriving in the same group
// within the same instant can't both observe "not in cooldown" and both fire
// (plan §9.2.2 B3).
var aiCooldownMap sync.Map // topic -> time.Time

func tryAcquireAiCooldown(topic string, cooldownSec int) bool {
	now := time.Now()
	for {
		prevVal, loaded := aiCooldownMap.Load(topic)
		if !loaded {
			if _, actuallyLoaded := aiCooldownMap.LoadOrStore(topic, now); !actuallyLoaded {
				return true // we placed the first marker for this topic
			}
			continue // lost the race to another goroutine; re-read and retry
		}
		prev := prevVal.(time.Time)
		if now.Sub(prev) < time.Duration(cooldownSec)*time.Second {
			return false // still within cooldown
		}
		if aiCooldownMap.CompareAndSwap(topic, prevVal, now) {
			return true // atomically claimed this dispatch
		}
		// CAS lost to a concurrent goroutine; retry against its fresher value.
	}
}

// pubRateWindow is the fixed window tryAcquirePublish resets on. Not
// operator-configurable like the count is (CurrentRuntimeConfig().
// PublishRateLimitPer10s) -- the window shape is an implementation detail,
// the per-window budget is the operator-facing knob.
const pubRateWindow = 10 * time.Second

// pubRateEntry is one uid's fixed-window publish counter. Its own mutex
// (rather than relying on pubRateLimiter's map-level concurrency safety)
// guards the read-modify-write of windowStart/count against two publishes
// from the same uid landing on different goroutines at once -- sync.Map only
// makes the map itself safe to share, not the struct a Load returns.
type pubRateEntry struct {
	mu          sync.Mutex
	windowStart time.Time
	count       int
}

// pubRateLimiter tracks every uid that has ever published, for the lifetime
// of the process. Never purged -- same tradeoff groupNameCache and
// aiCooldownMap already make in this file, and at a few dozen bytes per
// entry it would take an enormous, indefinitely-growing user base before
// this is a real memory concern.
var pubRateLimiter sync.Map // uid -> *pubRateEntry

// tryAcquirePublish reports whether uid may publish one more message right
// now, under a fixed-window limit of maxPerWindow per window. Mirrors
// tryAcquireAiCooldown's LoadOrStore-then-CAS-free-because-per-entry-locked
// shape: the entry itself, once created, is never replaced, only mutated
// under its own lock, so there is no lost-update window between two
// goroutines racing to create the first entry for a brand-new uid either.
func tryAcquirePublish(uid string, maxPerWindow int, window time.Duration) bool {
	v, _ := pubRateLimiter.LoadOrStore(uid, &pubRateEntry{windowStart: time.Now()})
	e := v.(*pubRateEntry)
	e.mu.Lock()
	defer e.mu.Unlock()
	now := time.Now()
	if now.Sub(e.windowStart) >= window {
		e.windowStart = now
		e.count = 0
	}
	if e.count >= maxPerWindow {
		return false
	}
	e.count++
	return true
}

// dispatchAiQaTask handles one @小桃子 mention end to end: settings lookup,
// per-group cooldown, a synchronous HTTP call to roubao's internal endpoint
// (which does its own per-group-owner credit/quota/knowledge-scope checks —
// im-go deliberately does not duplicate the daily-quota check here, see plan
// §9.2.2 B2), and finally delivering either the AI's answer or a friendly
// failure notice back into the group. Always invoked via `go` (see
// handlePub) so a slow/timed-out LLM call never blocks message broadcast for
// the rest of the group.
func (h *Hub) dispatchAiQaTask(topic, askerUID, question string) {
	// This whole function runs detached via `go h.dispatchAiQaTask(...)`
	// (see handlePub) with no caller left to observe a panic. Go's default
	// behavior for an unrecovered panic in any goroutine — including one
	// started with a bare `go` — is to crash the entire process, not just
	// this goroutine. Unlike the rest of this file's request-handling code,
	// this path is now reachable by any group member simply sending a
	// message containing "@小桃子", and it does real HTTP/DB work
	// (GetGroupAiSettingsCached, AskGroupAiQa, deliverMessage) plus type
	// assertions inside those calls — any one of those failing in an
	// unexpected way must not take down every other user's connection.
	defer func() {
		if r := recover(); r != nil {
			logf("dispatchAiQaTask: recovered panic for topic=%s: %v", topic, r)
		}
	}()
	settings, err := h.GetGroupAiSettingsCached(topic)
	if err != nil || settings == nil || !settings.AiQaEnabled {
		return
	}
	if !tryAcquireAiCooldown(topic, settings.AiCooldownSec) {
		// No WS feedback is defined by the protocol contract for a
		// cooldown-suppressed mention; silently dropping matches the current
		// contract. A "你问得有点快" style notice is a documented P2 follow-up
		// (plan §7 P2 acceptance criteria), not part of this contract.
		return
	}

	h.BroadcastAny(topic, &AiTypingMessage{Type: TypeAiTyping, Topic: topic}, "")

	groupInfo, err := h.store.GetGroupInfo(topic)
	if err != nil || groupInfo == nil || groupInfo.Owner == "" {
		h.deliverAiFallback(topic, aiQaFailureMessage(""))
		return
	}
	// Always resolve the owner fresh from im-go's own store rather than
	// trusting any caller-supplied value — im-go is the authority on "who
	// owns this group right now" (plan §8.2 table, last row).
	ownerAppUserID, err := h.resolveAppUserID(groupInfo.Owner)
	if err != nil {
		h.deliverAiFallback(topic, aiQaFailureMessage(""))
		return
	}

	answer, ok, reason := h.internalAPI.AskGroupAiQa(topic, askerUID, question, ownerAppUserID, settings.AiKbIds, settings.AiDocIds)
	if !ok {
		h.deliverAiFallback(topic, aiQaFailureMessage(reason))
		return
	}
	if _, _, _, err := h.deliverMessage(topic, AiAssistantUID, SenderKindAiAssistant, answer, MsgTypeText, "", "", nil, 0, nil, ""); err != nil {
		logf("dispatchAiQaTask: deliver AI reply failed: %v", err)
	}
}

func (h *Hub) deliverAiFallback(topic, text string) {
	if _, _, _, err := h.deliverMessage(topic, AiAssistantUID, SenderKindAiAssistant, text, MsgTypeText, "", "", nil, 0, nil, ""); err != nil {
		logf("dispatchAiQaTask: deliver fallback failed: %v", err)
	}
}

// aiQaFailureMessage turns roubao's internal failure `reason` (per the
// protocol contract's enum) into a friendly group-chat message. Unknown/empty
// reasons — including transport failures, which AskGroupAiQa itself already
// normalizes to "timeout" — fall back to the same "还在整理资料" wording used for
// an actual timeout, since from the asker's point of view both look
// identical (no answer showed up in time).
func aiQaFailureMessage(reason string) string {
	switch reason {
	case "insufficient_credits":
		return "群主积分不足，小桃子暂时无法为本群解答问题"
	case "quota_exceeded":
		return "本群今日的小桃子问答次数已用完，请明天再试"
	case "not_configured":
		return "群主还没有为本群配置可用的知识库，请联系群主在群设置中勾选"
	case "owner_inactive":
		return "小桃子暂时无法为本群解答问题"
	case "rate_limited":
		return "问得有点快，请稍后再试"
	default:
		return "小桃子正在整理资料，请稍后再问"
	}
}

// ---------------------------------------------------------------------------
// IM 语音/视频通话 (IM_VOICE_VIDEO_CALL_PLAN.md §4)
//
// im-go owns real-time connection state and relationship/reachability
// authorization for calls; roubao (the main Node server) owns money and the
// persistent ImCallSession record -- exactly the split plan §1 decision 5
// draws from the group-AI-QA and red-packet precedents already in this file.
// Every business decision (can this call be created, what does answering it
// cost, has it gone on too long) is a synchronous round trip to roubao via
// InternalAPIClient; every real-time signaling decision (is the callee even
// reachable right now, who won the race to answer vs. reject vs. cancel,
// what happens when someone's socket just dropped) is decided here, in
// memory, because roubao has no visibility into live WebSocket connections
// at all.
// ---------------------------------------------------------------------------

// callState statuses. Deliberately NOT a mirror of ImCallSession.status's
// richer DB vocabulary (ringing/active/ended/missed/rejected/cancelled/busy/
// offline/failed) -- once a call reaches ANY terminal outcome from im-go's
// point of view it is removed from callHub immediately (see callHub.remove),
// so there is no reason for this in-memory state machine to distinguish
// *which* terminal outcome occurred; that distinction is carried instead in
// the "reason" string handed to SettleCallSession / put on the call_ended
// push, which is the one place a human or the DB actually needs it.
const (
	callStateRinging   = "ringing"
	callStateAnswering = "answering"
	callStateActive    = "active"
	callStateEnded     = "ended"
)

// callState is one call's server-side view: who's involved, what phase it's
// in, and (only while ringing) the timer that must be stopped on every path
// OTHER than the one that actually fires it.
//
// This is the first "every session gets its own timer" shape anywhere in
// im-go (plan §4.3's own callout) -- everything else in this file is either
// read-time lazy expiry (e.g. group QR tokens) or a periodic batch sweep
// (the *Reaper goroutines in main.go). A leaked, un-Stopped timer here does
// not merely waste memory the way it might elsewhere: it fires a real
// "missed" verdict on top of a call that may have been resolved, correctly,
// milliseconds earlier by an entirely different path (accept/reject/
// cancel), corrupting an outcome that already happened. resolve() below is
// the ONLY place status is allowed to change specifically so this can never
// be gotten wrong at one call site and right at another.
type callState struct {
	mu sync.Mutex

	id              string
	callerUID       string
	calleeUID       string
	callerAppUserID string
	calleeAppUserID string
	// callType is mutated in place by handleCallUpgradeAccept once
	// UpgradeCallSession confirms the upgrade server-side -- there is no
	// separate "pending upgrade" status; call_upgrade/call_upgrade_request
	// are relayed without touching callState at all (see handleCallUpgrade),
	// so an unanswered upgrade request simply times out on its own with no
	// state to clean up here.
	callType  string
	status    string
	ringTimer *time.Timer
}

// resolve is the only way callState.status is allowed to change. It
// succeeds (and mutates status) iff the call is currently in one of the
// listed `from` states; any other caller -- including two callers racing
// each other, or a caller arriving after the call already reached a
// DIFFERENT terminal state -- gets false and touches nothing. This is the
// in-memory CAS the plan calls for in place of a `WHERE status = $N` SQL
// update (there is no DB row to CAS against here): whichever goroutine wins
// the lock first and finds a matching `from` is the one and only winner.
//
// Stopping the ring timer is folded into every successful transition,
// unconditionally, rather than left to each caller to remember: Timer.Stop()
// on an already-fired or already-nil timer is a documented no-op, so this is
// safe to call from the ring-timeout callback's own resolve(ended, ringing)
// (stopping a timer from inside its own already-launched callback goroutine
// changes nothing) exactly as safely as from every other path that resolves
// a call while it is still ringing.
func (c *callState) resolve(to string, from ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	matched := false
	for _, f := range from {
		if c.status == f {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	c.status = to
	if c.ringTimer != nil {
		c.ringTimer.Stop()
	}
	return true
}

// currentTypeAndStatus is a small convenience for the handlers that need to
// read both without racing a concurrent resolve() -- e.g. handleCallUpgrade
// checking "is this call active, and is it not already video" before it
// bothers looking up and pushing to the peer.
func (c *callState) currentTypeAndStatus() (callType, status string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.callType, c.status
}

// setActiveType is called exactly twice in this file: once by
// handleCallAccept the instant AnswerCallSession confirms the call is live
// (moving status ringing->answering->active and possibly changing type to
// "voice" per AcceptAsVoice, all in one critical section so no other
// handler can observe the awkward "active but still holding the pre-accept
// type" in-between state), and once by handleCallUpgradeAccept after
// UpgradeCallSession confirms a voice->video upgrade (status is already
// "active" and stays "active" -- only callType changes).
//
// CAS-gated exactly like resolve() -- and for the same reason the struct's
// own doc comment gives for why resolve() must be the only place status
// changes. It didn't used to be: both call sites used to call an earlier,
// unconditional version of this method straight after a blocking HTTP round
// trip (AnswerCallSession / UpgradeCallSession) with no re-check of whether
// the call was still in the state they left it in. That round trip can take
// up to a few seconds, and Hub.Run's unregister branch (forceEndCallForDisconnect)
// runs on a completely separate goroutine -- if either participant's socket
// dropped during the wait, forceEndCallForDisconnect could resolve() this
// exact call to callStateEnded, tell Node to settle/stop billing on it, AND
// remove it from h.calls, all before the blocked handler's HTTP call
// returned. The unconditional version would then still hand out a real,
// unrevoked ARTC join credential and push call_answered/call_upgraded for a
// call Node had already marked terminal -- a callee could trigger this
// deliberately (accept, then force a sub-3-second disconnect+reconnect) to
// get free, unbilled conversation minutes, and the resurrected call would
// be unreachable by any further hangup/upgrade signaling since h.calls no
// longer has it. Now it succeeds (and mutates callType) iff the call is
// currently in one of the listed `from` states, exactly resolve()'s
// contract; any other caller gets false and touches nothing, and the
// caller is expected to treat that exactly like "someone else already
// closed this call" -- the same way the sibling failure branch just above
// each call site already does when resolve() itself loses that race.
func (c *callState) setActiveType(callType string, from ...string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	matched := false
	for _, f := range from {
		if c.status == f {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	c.status = callStateActive
	c.callType = callType
	return true
}

// peerOf returns the OTHER participant's uid, given one participant's own
// uid. Every call-ending/relaying handler needs exactly this to know who
// else to notify; it never needs the mutex since callerUID/calleeUID are
// set once at construction and never mutated afterwards.
func (c *callState) peerOf(uid string) string {
	if uid == c.calleeUID {
		return c.callerUID
	}
	return c.calleeUID
}

// callHub tracks every ringing/answering/active call by both keys other
// code needs to look one up by: the session id every client message after
// call_invite/call_ring carries, and the uid of either participant -- the
// only thing Hub.Run's unregister branch and a fresh call_invite's busy/
// reachability check have on hand. Both maps always point at the SAME
// *callState for a given call; there is no reference counting or ownership
// subtlety here because a call is installed under both keys atomically (see
// reserve) and removed from both together, under the same lock (see
// remove) -- it is never partially indexed.
type callHub struct {
	mu        sync.Mutex
	byUID     map[string]*callState
	bySession map[string]*callState
}

func newCallHub() *callHub {
	return &callHub{byUID: make(map[string]*callState), bySession: make(map[string]*callState)}
}

// reserve installs a freshly constructed call (status callStateRinging)
// under both maps in one atomic step with checking that NEITHER participant
// is already in a call. Doing the busy check and the insert as two separate
// operations would let two concurrent call_invites -- one legitimately
// targeting a free callee, one from that same callee dialing out at the
// same instant, say -- both pass the check before either has registered,
// and both proceed to call roubao's CreateCallSession: one of those Node-side
// sessions would then have no corresponding entry in this hub at all, unable
// to ever be settled by any path in this file. Returns false (and installs
// nothing) if either uid is already present in byUID.
func (ch *callHub) reserve(call *callState) bool {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if _, ok := ch.byUID[call.callerUID]; ok {
		return false
	}
	if _, ok := ch.byUID[call.calleeUID]; ok {
		return false
	}
	ch.byUID[call.callerUID] = call
	ch.byUID[call.calleeUID] = call
	ch.bySession[call.id] = call
	return true
}

func (ch *callHub) get(sessionID string) *callState {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.bySession[sessionID]
}

func (ch *callHub) getByUID(uid string) *callState {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	return ch.byUID[uid]
}

// remove evicts call from both maps. Compares by pointer identity, not by
// the uid/session-id keys alone -- same "identity, not string key"
// discipline Hub.Run's own unregister branch already applies to h.topics
// (see its doc comment): a caller holding a stale *callState must never
// evict whatever (possibly newer) call now legitimately owns that uid or
// session slot. In practice a given uid/session key is only ever reused
// after the previous call occupying it has already been removed, but this
// guard costs nothing and removes any doubt.
func (ch *callHub) remove(call *callState) {
	ch.mu.Lock()
	defer ch.mu.Unlock()
	if ch.byUID[call.callerUID] == call {
		delete(ch.byUID, call.callerUID)
	}
	if ch.byUID[call.calleeUID] == call {
		delete(ch.byUID, call.calleeUID)
	}
	if ch.bySession[call.id] == call {
		delete(ch.bySession, call.id)
	}
}

// settleCallAsync fires SettleCallSession off the hot path. By the time any
// caller reaches this, the CAS in resolve() has already succeeded and every
// live participant has already been told the call is over (their own ack,
// and/or the call_ended push) -- roubao's persistence of that fact is a
// courtesy to history/reporting, not something either participant is
// waiting on. Always launched via `go`, wrapping a recover() for the same
// reason dispatchAiQaTask does (the only other goroutine in this file doing
// real outbound HTTP with no caller left to observe a panic): an HTTP
// client call panicking should never happen, but nothing here should be
// able to take the whole process down if it somehow does.
func (h *Hub) settleCallAsync(callSessionID, reason string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logf("settleCallAsync: recovered panic for session=%s reason=%s: %v", callSessionID, reason, r)
			}
		}()
		if err := h.internalAPI.SettleCallSession(callSessionID, reason); err != nil {
			logf("[call] SettleCallSession(%s) failed for session=%s: %v", reason, callSessionID, err)
		}
	}()
}

// pushCallEnded notifies uid (if currently connected) that call has ended,
// via the shared call_ended CallSignalMessage shape every termination path
// in this section uses. Not finding a live connection is not logged as an
// error -- plan §1 decision 4/§4.3 give this feature no offline queue at
// all; a peer who isn't reachable right now finds out the call is gone the
// ordinary way any client discovers server-side state it missed (its next
// natural interaction), same as e2e_terminate/e2e_clear's own "best-effort,
// no queue" precedent already established in this file.
func (h *Hub) pushCallEnded(uid, callSessionID, reason string) {
	if c := h.GetClientByUID(uid); c != nil {
		c.SendAny(&CallSignalMessage{Type: ActionCallEnded, CallSessionId: callSessionID, Reason: reason})
	}
}

// handleCallInvite starts a new call. See the file-level comment above this
// section for the im-go/roubao split; see plan §1 decision 4 for why an
// unreachable callee fails instantly instead of ringing into the void.
func (h *Hub) handleCallInvite(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeAuth, "not authenticated"))
		return
	}
	calleeUID := msg.Topic
	if calleeUID == "" || !isUserUID(calleeUID) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeBadReq, "topic (callee uid) required"))
		return
	}
	if calleeUID == client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeBadReq, "cannot call yourself"))
		return
	}
	if msg.CallType != CallTypeVoice && msg.CallType != CallTypeVideo {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeBadReq, "callType must be voice or video"))
		return
	}

	// Decision 4: a callee with no live connection right now fails fast,
	// before any session is created on either side -- 45 seconds of ringing
	// nobody will ever see (im_fcm_tokens' own real-world reliability
	// history, cited in the plan, is why this isn't "ring anyway and let FCM
	// try") is worse than an honest "offline" this instant.
	if h.GetClientByUID(calleeUID) == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeConflict, "offline"))
		return
	}

	calleeAppUserID, err := h.resolveAppUserID(calleeUID)
	if err != nil {
		logf("[call] resolveAppUserID(%s) failed: %v", calleeUID, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeInternal, "查询失败，请重试"))
		return
	}

	call := &callState{
		id:              RandString(32),
		callerUID:       client.uid,
		calleeUID:       calleeUID,
		callerAppUserID: client.appUserID,
		calleeAppUserID: calleeAppUserID,
		callType:        msg.CallType,
		status:          callStateRinging,
	}
	if !h.calls.reserve(call) {
		// "busy" covers two distinct real situations under one wire reason:
		// the callee is already ringing/on another call (the plan's own
		// definition of call_busy), or -- not in the plan's table, but
		// required for this hub's own "one call per uid" invariant (plan
		// §9.3) to hold at all -- the CALLER is. Both are the same
		// underlying fact from im-go's point of view: this model has no
		// concept of a uid being in more than one call at once, so there is
		// no second, more specific reason string worth inventing for the
		// rarer self-busy case (a client sending a second invite while its
		// first is still ringing is a client-side bug/race, not a normal
		// user flow the UI is expected to hit).
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeConflict, "busy"))
		return
	}

	ok, reason, err := h.internalAPI.CreateCallSession(call.id, call.callerAppUserID, call.calleeAppUserID, call.callType)
	if err != nil {
		logf("[call] CreateCallSession failed: %v", err)
		h.calls.remove(call)
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeInternal, "发起通话失败，请稍后重试"))
		return
	}
	if !ok {
		h.calls.remove(call)
		// "not_friends" gets CodeAuth, matching resolveP2PTopic's own
		// existing "not friends" rejection elsewhere in this file --
		// everything else roubao can return here ("insufficient_credits",
		// or any future reason) is a business-state conflict rather than a
		// relationship/authorization failure, hence CodeConflict.
		code := CodeConflict
		if reason == "not_friends" {
			code = CodeAuth
		}
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, code, reason))
		return
	}

	// A disconnect on either side may have already force-ended this call
	// (forceEndCallForDisconnect, on Hub.Run's own goroutine, races freely
	// against this handler since call was reserved into h.calls before
	// CreateCallSession was even dispatched -- see reserve()'s own doc
	// comment) while that round trip -- a real network call, orders of
	// magnitude longer than any single in-process step -- was in flight. If
	// that already happened, the disconnect path has already done
	// everything there is to do here: resolved the call, removed it,
	// notified whichever peer was reachable, and called SettleCallSession.
	// This handler only needs to stop short of ringing a callee for a call
	// that's already over and tell the caller so -- re-settling would be a
	// second, redundant SettleCallSession call for the same session.
	//
	// One residual gap, not solved here: if the disconnect race is fast
	// enough that its own SettleCallSession HTTP call reaches roubao BEFORE
	// this CreateCallSession call's row-creating write does (two independent
	// HTTP requests, no cross-request ordering guarantee), roubao may settle
	// a session id it hasn't created yet and the row this CreateCallSession
	// call is about to insert would be left permanently "ringing" with
	// nothing left in im-go that will ever call SettleCallSession on it
	// again. This is the same class of gap CreateCallSession's own doc
	// comment already accepts for V1 (see internal_client.go) -- not
	// reintroducing it here, just re-flagging where else it surfaces.
	if _, status := call.currentTypeAndStatus(); status != callStateRinging {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallInvite, CodeConflict, "对方已离线"))
		return
	}

	ringTimeoutSeconds := CurrentRuntimeConfig().CallRingTimeoutSeconds
	call.ringTimer = time.AfterFunc(time.Duration(ringTimeoutSeconds)*time.Second, func() {
		h.handleCallRingTimeout(call)
	})

	callerName, callerAvatar, err := h.internalAPI.GetProfile(calleeAppUserID, call.callerAppUserID)
	if err != nil {
		logf("[call] GetProfile failed, ringing with a generic caller identity: %v", err)
	}

	if callee := h.GetClientByUID(calleeUID); callee != nil {
		callee.SendAny(&CallSignalMessage{
			Type: ActionCallRing, CallSessionId: call.id, CallType: call.callType,
			CallerName: callerName, CallerAvatar: callerAvatar,
			RingTimeoutSeconds: ringTimeoutSeconds,
		})
	} else {
		// The callee disconnected in the narrow window between the
		// reachability check above and here (their ReadPump/Disconnect runs
		// on an independent goroutine). h.unregister's forced-end branch
		// (Hub.Run) is already racing to settle this exact call the instant
		// their unregister is processed -- call was reserved into h.calls
		// before this point specifically so that race is caught -- so there
		// is nothing further to do here beyond not crashing on a nil client.
		logf("[call] callee %s disconnected between reachability check and ring push for session %s", calleeUID, call.id)
	}

	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallInvite, Code: CodeOK, Topic: msg.Topic, CallSessionId: call.id})
	logf("[call] %s invited %s (session=%s type=%s)", client.uid, calleeUID, call.id, call.callType)
}

// handleCallRingTimeout fires from call.ringTimer (time.AfterFunc, started
// in handleCallInvite) when nobody has acted on a ringing call within
// im_call.ring_timeout_seconds. Runs on its own goroutine (that's what
// AfterFunc gives you) -- same panic discipline as dispatchAiQaTask, the
// only other detached goroutine in this file doing real HTTP/DB work with no
// caller left to observe a failure.
func (h *Hub) handleCallRingTimeout(call *callState) {
	defer func() {
		if r := recover(); r != nil {
			logf("handleCallRingTimeout: recovered panic for session=%s: %v", call.id, r)
		}
	}()
	if !call.resolve(callStateEnded, callStateRinging) {
		// Someone else (accept/reject/cancel/disconnect) already resolved
		// this call. AfterFunc's own documentation is explicit that a timer
		// which has already started running its function cannot be
		// un-fired by a racing Stop() -- so this branch is a real,
		// reachable, and entirely expected no-op, not a bug.
		return
	}
	h.calls.remove(call)
	h.pushCallEnded(call.callerUID, call.id, "no_answer")
	h.pushCallEnded(call.calleeUID, call.id, "no_answer")
	h.settleCallAsync(call.id, "no_answer")
	logf("[call] session=%s ring timeout (no_answer)", call.id)
}

// handleCallAccept answers a ringing call. See ServerMessage's "IM 语音/视频
// 通话" block and AnswerCallSessionResult's doc comment for why the caller's
// and callee's ARTC tokens are two distinct values and must never be mixed
// up: this handler's own ack carries CalleeToken (the answering client IS
// the callee), while the call_answered push to the caller carries
// CallerToken.
func (h *Hub) handleCallAccept(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallAccept, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallAccept, CodeBadReq, "callSessionId required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	// call.calleeUID != client.uid also catches a caller trying to "accept"
	// their own outgoing call by guessing/reusing the session id -- same
	// "does this id actually belong to a topic/session THIS caller has
	// authority over" check handleGroupJoinReview already applies to
	// joinRequestId.
	if call == nil || call.calleeUID != client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallAccept, CodeNotFound, "通话不存在或已结束"))
		return
	}
	if !call.resolve(callStateAnswering, callStateRinging) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallAccept, CodeConflict, "该通话已被处理"))
		return
	}

	callType, _ := call.currentTypeAndStatus()
	effectiveType := callType
	if msg.AcceptAsVoice && callType == CallTypeVideo {
		effectiveType = CallTypeVoice
	}

	answer, err := h.internalAPI.AnswerCallSession(call.id, effectiveType)
	if err != nil || !answer.Ok {
		reason := "internal_error"
		if err != nil {
			logf("[call] AnswerCallSession failed for session=%s: %v", call.id, err)
		} else {
			reason = answer.Reason
		}
		// The call never reached "active" -- both sides need to be told it
		// failed, exactly like any other im-go-decided termination path. But
		// only if THIS handler wins the resolve: the AnswerCallSession round
		// trip just blocked this goroutine for a real network call, during
		// which either participant's socket could have dropped and already
		// force-ended this exact call via Hub.Run's unregister branch. If
		// that already happened, resolve() correctly reports the loss here
		// and this handler must not re-settle (a second, contradictory
		// SettleCallSession call and a second, confusing call_ended push
		// with a different reason) a call someone else has already closed.
		if call.resolve(callStateEnded, callStateAnswering) {
			h.calls.remove(call)
			h.pushCallEnded(call.callerUID, call.id, "failed")
			h.settleCallAsync(call.id, "failed")
		}
		client.SendJSON(ErrorResponse(msg.Id, ActionCallAccept, CodeInternal, reason))
		return
	}

	// CAS re-check against forceEndCallForDisconnect (or a hangup) having won
	// while AnswerCallSession's HTTP round trip was in flight -- see
	// setActiveType's doc comment. If we lost, whoever got there first has
	// already removed the call from h.calls and settled/pushed call_ended;
	// we must not hand out a credential for a call that no longer exists.
	if !call.setActiveType(effectiveType, callStateAnswering) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallAccept, CodeConflict, "该通话已被处理"))
		return
	}

	client.SendJSON(&ServerMessage{
		Id: msg.Id, Action: ActionCallAccept, Code: CodeOK, CallSessionId: call.id,
		ChannelId: answer.ChannelId, RtcToken: answer.CalleeToken, AppId: answer.AppId,
		VideoQuality: answer.VideoQuality, DenoiseEnabled: answer.DenoiseEnabled,
	})
	if caller := h.GetClientByUID(call.callerUID); caller != nil {
		caller.SendAny(&CallSignalMessage{
			Type: ActionCallAnswered, CallSessionId: call.id,
			ChannelId: answer.ChannelId, RtcToken: answer.CallerToken, AppId: answer.AppId,
			VideoQuality: answer.VideoQuality, DenoiseEnabled: answer.DenoiseEnabled,
		})
	}
	logf("[call] session=%s answered by %s (type=%s)", call.id, client.uid, effectiveType)
}

// handleCallReject declines a ringing call -- callee-only, before anyone
// has answered. See plan §4.3's state diagram: "ringing --(callee
// call_reject)--> rejected".
func (h *Hub) handleCallReject(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallReject, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallReject, CodeBadReq, "callSessionId required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	if call == nil || call.calleeUID != client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallReject, CodeNotFound, "通话不存在或已结束"))
		return
	}
	if !call.resolve(callStateEnded, callStateRinging) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallReject, CodeConflict, "该通话已被处理"))
		return
	}
	h.calls.remove(call)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallReject, Code: CodeOK, CallSessionId: call.id})
	h.pushCallEnded(call.callerUID, call.id, "rejected")
	h.settleCallAsync(call.id, "rejected")
	logf("[call] session=%s rejected by %s", call.id, client.uid)
}

// handleCallCancel withdraws an outgoing call before the callee has
// answered -- caller-only. Plan §4.3: "ringing --(caller call_cancel)-->
// cancelled". Once the call has moved past ringing (answering/active), a
// caller wanting out uses call_hangup instead -- resolve() below only
// matches callStateRinging on purpose, so a cancel arriving a moment too
// late (racing an in-flight accept) is correctly rejected rather than
// retroactively undoing an answer already in progress.
func (h *Hub) handleCallCancel(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallCancel, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallCancel, CodeBadReq, "callSessionId required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	if call == nil || call.callerUID != client.uid {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallCancel, CodeNotFound, "通话不存在或已结束"))
		return
	}
	if !call.resolve(callStateEnded, callStateRinging) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallCancel, CodeConflict, "该通话已被接听或结束"))
		return
	}
	h.calls.remove(call)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallCancel, Code: CodeOK, CallSessionId: call.id})
	h.pushCallEnded(call.calleeUID, call.id, "cancelled")
	h.settleCallAsync(call.id, "cancelled")
	logf("[call] session=%s cancelled by %s", call.id, client.uid)
}

// handleCallHangup ends a call that has moved past ringing -- either
// participant, from "answering" (the narrow window between call_accept and
// the caller receiving call_answered -- see handleCallCancel's comment for
// why a hangup here, not a cancel, is the correct action once accept is
// already in flight) or "active".
func (h *Hub) handleCallHangup(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallHangup, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallHangup, CodeBadReq, "callSessionId required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	if call == nil || (call.callerUID != client.uid && call.calleeUID != client.uid) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallHangup, CodeNotFound, "通话不存在或已结束"))
		return
	}
	if !call.resolve(callStateEnded, callStateAnswering, callStateActive) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallHangup, CodeConflict, "通话已结束"))
		return
	}
	h.calls.remove(call)
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallHangup, Code: CodeOK, CallSessionId: call.id})
	h.pushCallEnded(call.peerOf(client.uid), call.id, "user_hangup")
	h.settleCallAsync(call.id, "user_hangup")
	logf("[call] session=%s hung up by %s", call.id, client.uid)
}

// handleCallUpgrade relays a mid-call voice->video upgrade request to the
// peer -- pure signaling, no roubao call and no callState mutation at all
// (plan §4.4: UpgradeCallSession is only ever called from
// handleCallUpgradeAccept, once the peer has actually agreed). An
// unanswered upgrade request simply has nothing further happen; there is no
// timeout or pending-upgrade state to clean up here.
func (h *Hub) handleCallUpgrade(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgrade, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" || msg.ToType != CallTypeVideo {
		// V1 only ever upgrades voice -> video (plan §1 decision 3).
		// Lowering back to voice is never a call_upgrade round trip at all --
		// closing the local camera mid-video-call is a device-local toggle
		// (plan §7.5) that never touches the server.
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgrade, CodeBadReq, "callSessionId and toType=video required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	if call == nil || (call.callerUID != client.uid && call.calleeUID != client.uid) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgrade, CodeNotFound, "通话不存在或已结束"))
		return
	}
	callType, status := call.currentTypeAndStatus()
	if status != callStateActive {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgrade, CodeConflict, "通话尚未接通"))
		return
	}
	if callType == CallTypeVideo {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgrade, CodeConflict, "已经是视频通话"))
		return
	}
	peerUID := call.peerOf(client.uid)
	peer := h.GetClientByUID(peerUID)
	if peer == nil {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgrade, CodeConflict, "对方已离线"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallUpgrade, Code: CodeOK, CallSessionId: call.id})
	peer.SendAny(&CallSignalMessage{Type: ActionCallUpgradeRequest, CallSessionId: call.id, ToType: CallTypeVideo})
	logf("[call] session=%s upgrade requested by %s", call.id, client.uid)
}

// handleCallUpgradeAccept commits a voice->video upgrade the peer has just
// agreed to. Pushed to BOTH participants (including the accepter
// themselves, plan §4.2's "call_upgraded ... 双端") because both sides need
// the same videoQuality value to configure their own local video encoder
// before either starts actually publishing a video track.
func (h *Hub) handleCallUpgradeAccept(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeBadReq, "callSessionId required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	if call == nil || (call.callerUID != client.uid && call.calleeUID != client.uid) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeNotFound, "通话不存在或已结束"))
		return
	}
	if _, status := call.currentTypeAndStatus(); status != callStateActive {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeConflict, "通话已结束"))
		return
	}

	result, err := h.internalAPI.UpgradeCallSession(call.id, CallTypeVideo)
	if err != nil {
		logf("[call] UpgradeCallSession failed for session=%s: %v", call.id, err)
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeInternal, "升级失败，请重试"))
		return
	}
	if !result.Ok {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeConflict, result.Reason))
		return
	}
	// Same CAS re-check as handleCallAccept, against the identical race on the
	// UpgradeCallSession round trip -- see setActiveType's doc comment.
	if !call.setActiveType(CallTypeVideo, callStateActive) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeAccept, CodeConflict, "通话已结束"))
		return
	}

	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallUpgradeAccept, Code: CodeOK, CallSessionId: call.id})
	upgraded := &CallSignalMessage{Type: ActionCallUpgraded, CallSessionId: call.id, VideoQuality: result.VideoQuality}
	client.SendAny(upgraded)
	if peer := h.GetClientByUID(call.peerOf(client.uid)); peer != nil {
		peer.SendAny(upgraded)
	}
	logf("[call] session=%s upgraded to video (accepted by %s)", call.id, client.uid)
}

// handleCallUpgradeReject declines a mid-call voice->video upgrade request.
// The call itself is entirely unaffected -- it stays exactly as it was
// (voice, active), no callState mutation, no roubao call at all: declining
// only tells the requester no, plan §7.6's own description of this ("通话
// 继续保持语音状态不受影响").
func (h *Hub) handleCallUpgradeReject(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeReject, CodeAuth, "not authenticated"))
		return
	}
	if msg.CallSessionId == "" {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeReject, CodeBadReq, "callSessionId required"))
		return
	}
	call := h.calls.get(msg.CallSessionId)
	if call == nil || (call.callerUID != client.uid && call.calleeUID != client.uid) {
		client.SendJSON(ErrorResponse(msg.Id, ActionCallUpgradeReject, CodeNotFound, "通话不存在或已结束"))
		return
	}
	client.SendJSON(&ServerMessage{Id: msg.Id, Action: ActionCallUpgradeReject, Code: CodeOK, CallSessionId: call.id})
	if peer := h.GetClientByUID(call.peerOf(client.uid)); peer != nil {
		peer.SendAny(&CallSignalMessage{Type: ActionCallUpgradeDeclined, CallSessionId: call.id})
	}
	logf("[call] session=%s upgrade declined by %s", call.id, client.uid)
}

// scheduleForceEndCallForDisconnect is Hub.Run's unregister branch's actual
// hook into this section now (see the call site's own comment for the
// "!superseded only" discipline) -- forceEndCallForDisconnect itself is only
// called from the timer this schedules, or directly by tests.
//
// Real-world disconnects during a call are not exclusively "this uid is
// gone for good": on VIVO OriginOS (and other OEM proprietary background
// managers layered on top of stock Android, independent of any standard
// permission the app can hold), receiving a call can get the app
// backgrounded, which trips ImWebSocketClient's own staleness watchdog into
// tearing down and rebuilding the socket -- a graceful close (code 1000)
// immediately followed by a reconnect that is a real, if brief, gap rather
// than an instant supersede. Hub.Run's existing "superseded" check only
// catches the case where the replacement connection is already registered
// BEFORE the old one's unregister is processed; it cannot catch a reconnect
// that lands a few seconds later, which is exactly the shape of this OEM
// throttling gap. Ending the call instantly on that gap is what produced
// "connects, then hangs up immediately" for the affected callee -- the call
// was force-ended before the reconnect had any chance to land.
//
// This does not touch genuine "callee is truly gone" cases: no call, no
// timer gets scheduled at all (the lookup below is a no-op for the common
// case of a disconnect with nothing to protect), and if the reconnect never
// comes, callDisconnectGraceDuration later the call ends exactly as before,
// just delayed by a bounded, short window.
func (h *Hub) scheduleForceEndCallForDisconnect(uid string) {
	if h.calls.getByUID(uid) == nil {
		// No call in flight for this uid -- by far the common case for any
		// disconnect. Nothing to protect, nothing to schedule.
		return
	}
	if existing, ok := h.pendingCallDisconnects.LoadAndDelete(uid); ok {
		// A prior disconnect for this same uid already has a timer running
		// (e.g. a flapping connection: disconnect, reconnect, disconnect
		// again before the first timer fired). Replace it with a fresh
		// full-length grace period rather than letting the stale one fire
		// early against the current gap.
		existing.(*time.Timer).Stop()
	}
	timer := time.AfterFunc(callDisconnectGraceDuration, func() {
		h.pendingCallDisconnects.Delete(uid)
		if h.GetClientByUID(uid) != nil {
			// Reconnected in time but somehow missed cancelPendingCallDisconnect
			// (e.g. this fired the instant the register case's own cancel ran,
			// losing the race by a hair) -- re-check here rather than trust
			// the cancellation alone.
			return
		}
		h.forceEndCallForDisconnect(uid)
	})
	h.pendingCallDisconnects.Store(uid, timer)
}

// cancelPendingCallDisconnect is Hub.Run's register branch's hook back into
// this section: uid is live again (fresh login or reconnect), so any
// scheduleForceEndCallForDisconnect grace timer still pending for it should
// stop rather than run out and re-check GetClientByUID on its own. A no-op
// (LoadAndDelete miss) for the overwhelming majority of registers, which
// never had a call-disconnect timer pending in the first place.
func (h *Hub) cancelPendingCallDisconnect(uid string) {
	if pending, ok := h.pendingCallDisconnects.LoadAndDelete(uid); ok {
		pending.(*time.Timer).Stop()
	}
}

// forceEndCallForDisconnect is scheduleForceEndCallForDisconnect's delayed
// action (or called directly by tests). uid has gone offline and stayed
// offline through the full reconnect grace period -- if they were ringing,
// answering, or actively on a call, the other party needs to hear about it
// now rather than being left waiting on someone who is never coming back
// this session.
func (h *Hub) forceEndCallForDisconnect(uid string) {
	call := h.calls.getByUID(uid)
	if call == nil {
		return
	}
	if !call.resolve(callStateEnded, callStateRinging, callStateAnswering, callStateActive) {
		return
	}
	h.calls.remove(call)
	h.pushCallEnded(call.peerOf(uid), call.id, "disconnected")
	h.settleCallAsync(call.id, "disconnected")
	logf("[call] session=%s force-ended: %s disconnected", call.id, uid)
}

// releaseCallSlotForBroadcastEnd clears this hub's own in-memory callHub
// state (the "busy" slot under both participants' uids) for the call
// currently associated with uid, WITHOUT pushing any additional call_ended
// signal or re-invoking SettleCallSession -- roubao has already made both of
// those happen on its own before ever calling InternalCallBroadcast (see
// that handler's own doc comment: the payload it forwards byte-for-byte IS
// one of those two pushes, and imCallBilling.ts's notifyCallEnded() already
// calls broadcastImCallUpdate for BOTH callerId and calleeId, with
// settleImCallSession() having already run before this HTTP request was
// even made). Reusing forceEndCallForDisconnect here would be wrong for
// exactly that reason -- it ALSO pushes a second call_ended (carrying an
// unrelated "disconnected" reason) to the peer and re-triggers
// SettleCallSession, duplicating and contradicting work roubao has already
// done correctly.
//
// Without this, a call roubao proactively ends on its own initiative
// (credit exhausted mid-call, max duration reached, or roubao judging this
// im-go process itself unreachable -- see imCallBilling.ts's runTick())
// leaves this hub's own callHub entry stuck under both participants' uids
// until one of their sockets actually disconnects or the process restarts:
// any call_invite either of them sends in between is rejected here as
// CodeConflict/"busy" even though roubao's own ImCallSession row is already
// a clean terminal state.
func (h *Hub) releaseCallSlotForBroadcastEnd(uid string) {
	call := h.calls.getByUID(uid)
	if call == nil {
		return
	}
	if !call.resolve(callStateEnded, callStateRinging, callStateAnswering, callStateActive) {
		return
	}
	h.calls.remove(call)
	logf("[call] session=%s call slot released via broadcast (roubao-initiated end)", call.id)
}

// subscribeTopic registers client under topic in the hub's topic index, so
// BroadcastToTopic can reach it in O(topic size) instead of scanning every
// connected client.
func (h *Hub) subscribeTopic(topic string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m, ok := h.topics[topic]
	if !ok {
		m = make(map[string]*Client)
		h.topics[topic] = m
	}
	m[client.uid] = client
}

// unsubscribeTopic removes client from the topic index, dropping the topic
// entry entirely once its last subscriber leaves.
func (h *Hub) unsubscribeTopic(topic string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if m, ok := h.topics[topic]; ok {
		delete(m, client.uid)
		if len(m) == 0 {
			delete(h.topics, topic)
		}
	}
}

// BroadcastToTopic sends msg to every client currently subscribed to topic,
// via the topic index rather than scanning every connected client — the
// previous version cost O(total online users) per message regardless of how
// many people were actually in the conversation.
func (h *Hub) BroadcastToTopic(topic string, msg *ServerMessage, excludeUID string) {
	h.broadcastRaw(topic, msg, excludeUID)
}

// BroadcastAny is BroadcastToTopic's counterpart for the "type"-discriminated
// wire structs (AiTypingMessage, ...) that don't fit ServerMessage's shape —
// mirrors the SendJSON/SendAny split on Client.
func (h *Hub) BroadcastAny(topic string, msg interface{}, excludeUID string) {
	h.broadcastRaw(topic, msg, excludeUID)
}

// broadcastRaw is the shared body of BroadcastToTopic/BroadcastAny.
//
// F-12/F-13：改前两者都在**持着 h.mu 的循环里**对每个收件人各 SendJSON 一次，
// 而 SendJSON/SendAny 内部各自 json.Marshal 一遍同一个对象。一条 500 人群的
// 消息因此要序列化 500 次相同的 JSON，全程占着那把全局读锁 —— 而
// subscribeTopic / unsubscribeTopic / Run 的注册注销分支要的是写锁，会被整段
// 排在后面。序列化一次、循环里只做 chan 投递，把锁内工作量压回 O(收件人数)
// 的纯内存写。
//
// marshal 刻意放在取锁**之前**：它是这里唯一可能耗时的一步，没有任何理由
// 在锁内做。
func (h *Hub) broadcastRaw(topic string, v interface{}, excludeUID string) {
	data, err := json.Marshal(v)
	if err != nil {
		logf("broadcastRaw: marshal failed for topic=%s: %v", topic, err)
		return
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for uid, client := range h.topics[topic] {
		if uid != excludeUID {
			client.SendRaw(data)
		}
	}
}

// BroadcastPresence notifies only the users who share a topic (P2P or
// group) with uid, instead of every connected client. The previous version
// broadcast every login/logout to the entire server regardless of any
// relationship, which scales O(total online users) per connect/disconnect —
// on a server with thousands of concurrently online users this turns
// ordinary mobile-network churn into a presence storm. Any gap from a peer
// not yet having re-subscribed this session is self-healed by the presence
// snapshot handleSub already sends on subscribe (see resolveP2PTopic).
// maxPresQueryUIDs bounds one `pres` query. The `*` form fans out over every
// peer sharing any topic, which for a user in large groups is unbounded.
const maxPresQueryUIDs = 500

// handlePresQuery answers a client-initiated presence snapshot request.
//
// Presence used to be push-only — ProcessMessage had no ActionPres case, so a
// client sending `pres` got "unknown action" back. That meant a client which
// missed a transition (or was handed a wrong one) had no way to ask again: the
// only pull-style read in the entire system was the snapshot handleSub happens
// to send, and every client-side path that would re-send `sub` is suppressed by
// a process-lifetime dedup set. This is the missing half.
//
//	{"action":"pres","topic":"*"}                  -> all peers
//	{"action":"pres","topics":["uid_a","uid_b"]}   -> just these
//
// The reply is ONE frame with a states array, not one frame per uid.
func (h *Hub) handlePresQuery(client *Client, msg *ClientMessage) {
	if !client.IsAuthenticated() {
		client.SendJSON(ErrorResponse(msg.Id, ActionPres, CodeAuth, "not authenticated"))
		return
	}

	var uids []string
	switch {
	case len(msg.Topics) > 0:
		uids = msg.Topics
	case msg.Topic == TopicAll:
		peers, err := h.store.GetPeerUIDs(client.uid)
		if err != nil {
			logf("handlePresQuery: GetPeerUIDs(%s) failed: %v", client.uid, err)
			client.SendJSON(ErrorResponse(msg.Id, ActionPres, CodeInternal, "presence lookup failed"))
			return
		}
		uids = peers
	case msg.Topic != "":
		uids = []string{msg.Topic}
	default:
		client.SendJSON(ErrorResponse(msg.Id, ActionPres, CodeBadReq, "topic or topics required"))
		return
	}

	// Only ever report on user uids, and never on the caller themselves.
	filtered := make([]string, 0, len(uids))
	seen := make(map[string]bool, len(uids))
	for _, u := range uids {
		if u == "" || u == client.uid || seen[u] || !isUserUID(u) {
			continue
		}
		seen[u] = true
		filtered = append(filtered, u)
		if len(filtered) >= maxPresQueryUIDs {
			logf("handlePresQuery: %s capped at %d uids", client.uid, maxPresQueryUIDs)
			break
		}
	}

	lastSeen, err := h.store.GetLastSeenBatch(filtered)
	if err != nil {
		// Not fatal: online/offline is the answer the caller actually needs,
		// last-seen is decoration. Report what we have rather than failing.
		logf("handlePresQuery: GetLastSeenBatch failed: %v", err)
		lastSeen = map[string]int64{}
	}

	states := make([]PresenceState, 0, len(filtered))
	for _, u := range filtered {
		online := h.GetClientByUID(u) != nil
		states = append(states, PresenceState{
			UID:        u,
			Online:     online,
			PresRev:    h.currentPresRev(u),
			LastSeenAt: lastSeen[u],
		})
	}
	client.SendJSON(&ServerMessage{
		Action: ActionPres, Code: CodeOK, What: PresSnapshot, States: states,
	})
}

// notifyContactSync tells both parties of a friendship change over their IM
// socket. `from` carries the OTHER party's uid, because that is the key the
// Android client uses for a 1:1 conversation — the receiver needs to know
// which conversation changed, not who triggered it.
//
// Best-effort by design: a client that is offline right now reconciles on its
// next contacts fetch. What this removes is the case where an IM user has a
// perfectly healthy /ws/im socket and still learns nothing, because the only
// notification for this event rode a different socket entirely.
func (h *Hub) notifyContactSync(uidA, uidB, event string) {
	for _, p := range [2][2]string{{uidA, uidB}, {uidB, uidA}} {
		if c := h.GetClientByUID(p[0]); c != nil {
			c.SendJSON(&ServerMessage{Action: ActionContactSync, What: event, From: p[1]})
		}
	}
}

// presenceEvent is one presence transition awaiting fan-out.
type presenceEvent struct {
	uid    string
	status string
}

// runPresenceWorker serializes presence fan-out. Started once from main().
//
// The previous shape was two bare `go h.BroadcastPresence(...)` calls — one
// from the register case, one from unregister. Each begins with a Postgres
// round trip (GetUserTopics), whose latency variance is orders of magnitude
// larger than the microseconds between the two `go` statements, so a single
// reconnect's ON and OFF had no ordering guarantee whatsoever. There is also
// no sequence number, timestamp or generation in the pres payload for a
// client to arbitrate with, and no periodic tick to correct a wrong value —
// so whenever OFF happened to land last, every peer latched "offline" for a
// user who was online, permanently. Draining a single channel restores
// happens-before between transitions for the same uid.
func (h *Hub) runPresenceWorker() {
	for ev := range h.presenceCh {
		// Stamp last_seen_at before announcing, so a peer that reacts to the
		// "offline" frame by querying a snapshot already sees the timestamp.
		if ev.status == PresOffline {
			if err := h.store.TouchLastSeen(ev.uid); err != nil {
				logf("TouchLastSeen(%s) failed: %v", ev.uid, err)
			}
		}
		h.BroadcastPresence(ev.uid, ev.status)
	}
}

// enqueuePresence hands a transition to the worker. It must never block: the
// only callers are inside Hub.Run, and blocking there is the same class of
// bug as calling Disconnect() under h.mu. If the (deep) queue is ever
// saturated we drop and say so loudly rather than wedge the hub.
func (h *Hub) enqueuePresence(uid, status string) {
	select {
	case h.presenceCh <- presenceEvent{uid: uid, status: status}:
	default:
		logf("WARN presence queue saturated, dropped %s for %s", status, uid)
	}
}

func (h *Hub) BroadcastPresence(uid string, status string) {
	topics, err := h.store.GetUserTopics(uid)
	if err != nil {
		logf("BroadcastPresence: GetUserTopics(%s) failed: %v", uid, err)
		return
	}
	msg := &ServerMessage{
		Action: ActionPres, Topic: uid, What: status,
		Online: status == PresOnline, PresRev: h.nextPresRev(uid),
	}
	// 与 broadcastRaw 同一处缺陷（F-12/F-13）：这里原本也是持锁逐人 marshal。
	// 这条路径其实是三者里最频繁的 —— 每一次连接建立/断开都要向该用户的所有
	// 对端各发一帧，而移动网络的抖动会让它持续发生。
	data, err := json.Marshal(msg)
	if err != nil {
		logf("BroadcastPresence: marshal failed for %s: %v", uid, err)
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	notified := make(map[string]bool, len(topics))
	for _, t := range topics {
		for peerUID, client := range h.topics[t.Topic] {
			if peerUID == uid || notified[peerUID] {
				continue
			}
			notified[peerUID] = true
			client.SendRaw(data)
		}
	}
}

func (h *Hub) GetClientByUID(uid string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[uid]
}

// resolveAppUserID maps an im-go uid back to the original app user UUID.
// Prefers the live connection (if the peer is online, this is free) and
// falls back to the im_users table otherwise.
//
// c is *another* connection's Client, not the caller's own — this call
// commonly runs on a goroutine handling some other client's call_invite/
// call_upgrade message while c's own login handler may be concurrently
// writing c.appUserID on its own goroutine. Must go through the c.mu-guarded
// accessor, not a bare field read (see AppUserID()'s doc comment).
func (h *Hub) resolveAppUserID(uid string) (string, error) {
	if c := h.GetClientByUID(uid); c != nil {
		if appUserID := c.AppUserID(); appUserID != "" {
			return appUserID, nil
		}
	}
	return h.store.GetAppUserID(uid)
}

// syncP2PPeer subscribes the peer's live connection (if currently online) to
// the given topic. EnsureP2PTopic only persists DB membership; it does not
// touch the peer's in-memory Client.subscribed set, which is what
// BroadcastToTopic actually checks — without this, a peer who is connected
// but hasn't independently opened this exact conversation yet gets neither
// the real-time push nor the offline FCM fallback (pushOfflineRecipients
// skips anyone with a live connection). Returns whether the peer is online.
func (h *Hub) syncP2PPeer(peerUID, topic string) bool {
	peerClient := h.GetClientByUID(peerUID)
	if peerClient == nil {
		return false
	}
	peerClient.Subscribe(topic)
	return true
}

// ensureTopicMember 校验请求方确实是该 topic 的成员，用于一切**非 uid 形态**的 topic
// （grp_… 与 canonical p2p_…）。这些分支此前完全没有授权检查：任何已登录用户只要
// 拿到 topic 串，就能订阅其实时消息、拉取全部历史、往里发消息、乃至硬删整个会话。
// 见 docs/IM_CROSS_TOPIC_LEAK_PLAN.md 的 S1–S4。
//
// 复用 IsGroupMember 是有意的：它查的是 im_topic_members 且不带 is_group 过滤，
// 所以对 grp_ 与 p2p_ 同样成立（EnsureP2PTopic 会把 1:1 会话的双方都写进这张表，
// 因此点击离线通知带 canonical p2p_ 串进来的合法成员不会被误拒）。
//
// DB 出错一律 fail-closed。此前 handlePub 写的是 err == nil && !isMember，
// 查询一失败就放行——这类判据会在故障时把整个授权检查静默跳过。
func (h *Hub) ensureTopicMember(client *Client, msg *ClientMessage, action, topic string) bool {
	isMember, err := h.store.IsGroupMember(topic, client.uid)
	if err != nil {
		logf("[%s] membership check failed uid=%s topic=%s: %v", action, client.uid, topic, err)
		client.SendJSON(ErrorResponse(msg.Id, action, CodeInternal, "membership check failed"))
		return false
	}
	if !isMember {
		client.SendJSON(ErrorResponse(msg.Id, action, CodeAuth, "not a member of this conversation"))
		return false
	}
	return true
}

// ensureGroupManager 是 ensureTopicMember 的高一级：校验请求方是该群的**群主
// 或管理员**。管理员角色上线之前，「谁能改群/踢人/清空群记录」只有 owner 一个
// 判据，群主本人不在线时整个群没有任何人能维护。
//
// DB 出错一律 fail-closed，和 ensureTopicMember 同一条纪律：授权检查在故障时
// 静默放行，比功能不可用严重得多。
func (h *Hub) ensureGroupManager(client *Client, msg *ClientMessage, action, topic string) bool {
	canManage, err := h.store.CanManageGroup(topic, client.uid)
	if err != nil {
		logf("[%s] manage check failed uid=%s topic=%s: %v", action, client.uid, topic, err)
		client.SendJSON(ErrorResponse(msg.Id, action, CodeInternal, "权限校验失败，请重试"))
		return false
	}
	if !canManage {
		client.SendJSON(ErrorResponse(msg.Id, action, CodeAuth, "只有群主或管理员可以执行此操作"))
		return false
	}
	return true
}

// isSubscribedInIndex 报告这条**连接本身**是否已登记在 h.topics[topic] 里。
//
// 比的是 client 指针而不是 uid：重连期间同一个 uid 短暂存在两个 *Client，按
// uid 判断会让已经被顶替的旧连接也算「已订阅」。这与 Run 的 unregister 分支
// 里那条「必须按连接身份而非 uid 清理」是同一条教训。
func (h *Hub) isSubscribedInIndex(topic string, client *Client) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.topics[topic]
	if !ok {
		return false
	}
	return m[client.uid] == client
}

// resolveP2PTopic resolves (creating on first contact) the canonical topic
// for a P2P conversation between client and peerUID. Every call that isn't
// already backed by a live membership row is gated on an accepted-friend
// check via the main server, since im-go's own DB connection has no access
// to the contacts table (see internal_client.go). On any failure, the
// appropriate error response has already been sent to the client — callers
// should just return. On success, also syncs the peer's live connection into
// the topic so real-time delivery works even if the peer hasn't
// independently subscribed yet.
//
// The gate is deliberately "does the CALLER currently have a membership row"
// rather than "does the topic exist". im_topics rows are permanent (a
// removed friendship intentionally leaves history in place — see
// InternalContactSync), so gating on topic existence made the friend check
// run exactly once per pair, ever: after InternalContactSync revoked both
// sides' im_topic_members rows on an unfriend/block, the topic still existed,
// this function would skip straight to EnsureP2PTopic, and that unconditional
// upsert would silently re-insert the very membership rows that had just been
// revoked — reopening the conversation to a removed contact on their next
// message. Gating on membership means a revoked party fails the check again
// (IsGroupMember is false) and must clear a real AreFriends round trip before
// EnsureP2PTopic is allowed to run.
func (h *Hub) resolveP2PTopic(client *Client, reqID, action, peerUID string) (topic string, peerOnline bool, ok bool) {
	candidate := h.store.GetP2PTopic(client.uid, peerUID)
	isMember, err := h.store.IsGroupMember(candidate, client.uid)
	if err != nil {
		logf("IsGroupMember failed: %v", err)
		client.SendJSON(ErrorResponse(reqID, action, CodeInternal, "topic lookup failed"))
		return "", false, false
	}
	if !isMember {
		peerAppUserID, err := h.resolveAppUserID(peerUID)
		if err != nil {
			logf("resolveAppUserID(%s) failed: %v", peerUID, err)
			client.SendJSON(ErrorResponse(reqID, action, CodeAuth, "cannot verify friendship"))
			return "", false, false
		}
		friends, err := h.internalAPI.AreFriends(client.appUserID, peerAppUserID)
		if err != nil {
			logf("AreFriends check failed: %v", err)
			client.SendJSON(ErrorResponse(reqID, action, CodeInternal, "friendship check failed"))
			return "", false, false
		}
		if !friends {
			client.SendJSON(ErrorResponse(reqID, action, CodeAuth, "not friends"))
			return "", false, false
		}
	}

	actualTopic, err := h.store.EnsureP2PTopic(client.uid, peerUID)
	if err != nil {
		logf("EnsureP2PTopic failed: %v", err)
		client.SendJSON(ErrorResponse(reqID, action, CodeInternal, "topic creation failed"))
		return "", false, false
	}
	online := h.syncP2PPeer(peerUID, actualTopic)
	return actualTopic, online, true
}

func isUserUID(uid string) bool {
	return (len(uid) >= 4 && uid[:4] == "uid_") || (len(uid) >= 3 && uid[:3] == "usr")
}

func init() {
	rand.Seed(time.Now().UnixNano())
}

const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandString(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	return string(b)
}
