// Membership and friendship checks, and resolving direct-chat topics.

package main

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
		friends, err := h.host.AreFriends(client.appUserID, peerAppUserID)
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
