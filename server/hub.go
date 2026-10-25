package main

import (
	"math/rand"
	"sync"
	"time"

	"example.com/imkit/server/host"
	"example.com/imkit/server/push"
)

// hostAPI is what the core asks of the host. Modules hold the host
// capabilities they need themselves (callHost, aiHost).
type hostAPI interface {
	host.Relations
	host.Profiles
	host.Media
}

// Hub maintains the set of active clients and routes messages between them
type Hub struct {
	store  *Store
	auth   host.Identity
	pusher *push.Dispatcher // nil disables offline push
	// webPushHosts are the push services a Web Push subscription may name.
	webPushHosts push.Hosts
	// vapidKey is the Web Push public key strict login replies carry ("" when
	// Web Push is not configured).
	vapidKey   string
	host       hostAPI                       // friendship checks, profiles and media
	mods       *modules                      // installed optional modules
	callHost   host.CallSessions             // set by the call module
	aiHost     host.GroupAI                  // set by the group AI module
	clients    map[string]*Client            // uid -> client (one active session per user)
	topics     map[string]map[string]*Client // topic -> uid -> client, for O(topic size) broadcast fan-out
	mu         sync.RWMutex
	register   chan *Client
	unregister chan *Client

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

func NewHub(store *Store, auth host.Identity, pusher *push.Dispatcher, hostAPI hostAPI) *Hub {
	return &Hub{
		store:      store,
		auth:       auth,
		pusher:     pusher,
		host:       hostAPI,
		clients:    make(map[string]*Client),
		topics:     make(map[string]map[string]*Client),
		register:   make(chan *Client, 100),
		unregister: make(chan *Client, 100),
		// Deep enough that ordinary mobile-network churn never reaches the
		// drop path; enqueuePresence deliberately drops rather than blocking
		// the hub loop if it ever does.
		presenceCh: make(chan presenceEvent, 1024),
		presRev:    make(map[string]int64),
		presEpoch:  time.Now().UnixMilli(),
		calls:      newCallHub(),
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
	case ActionAckTopicCleared:
		h.handleAckTopicCleared(client, msg)
	case actionAckGroupEvent:
		h.handleAckPendingGroupEvent(client, msg)
	default:
		// Module actions; a missing module answers "module not enabled".
		if !h.mods.dispatch(client, msg) {
			client.SendJSON(ErrorResponse(msg.Id, msg.Action, CodeBadReq, "unknown action: "+msg.Action))
		}
	}
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
