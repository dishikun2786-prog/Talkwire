// Presence: revision counter, serialized broadcast worker and batch queries.

package main

import (
	"encoding/json"
)

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
	// shared is set when only users sharing a conversation with the caller
	// may be reported (strict profile, red line R22).
	var shared map[string]bool
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
	if strictProtocol && msg.Topic != TopicAll {
		// The reference server answers about any uid, strangers included
		// (F30): anyone could poll whether anyone is online. Under the
		// strict profile only users sharing a conversation are reported;
		// the others are dropped as if they were not uids.
		peers, err := h.store.GetPeerUIDs(client.uid)
		if err != nil {
			logf("handlePresQuery: GetPeerUIDs(%s) failed: %v", client.uid, err)
			client.SendJSON(ErrorResponse(msg.Id, ActionPres, CodeInternal, "presence lookup failed"))
			return
		}
		shared = make(map[string]bool, len(peers))
		for _, p := range peers {
			shared[p] = true
		}
	}

	// Only ever report on user uids, and never on the caller themselves.
	filtered := make([]string, 0, len(uids))
	seen := make(map[string]bool, len(uids))
	for _, u := range uids {
		if u == "" || u == client.uid || seen[u] || !isUserUID(u) || (shared != nil && !shared[u]) {
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
		online := h.isOnline(u)
		states = append(states, PresenceState{
			UID:        u,
			Online:     online,
			PresRev:    h.currentPresRev(u),
			LastSeenAt: lastSeen[u],
		})
	}
	reply := &ServerMessage{Action: ActionPres, Code: CodeOK, What: PresSnapshot, States: states}
	if strictProtocol {
		reply.Id = msg.Id // F1
	}
	client.SendJSON(reply)
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
	// Every device of every peer, once each; never the user's own devices.
	notified := make(map[connKey]bool, len(topics))
	for _, t := range topics {
		for key, client := range h.topics[t.Topic] {
			if key.uid == uid || notified[key] {
				continue
			}
			notified[key] = true
			client.SendRaw(data)
		}
	}
}
