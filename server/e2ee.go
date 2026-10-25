// End-to-end encryption relay (optional module): opaque envelopes, key bundles, group encryption flag.

package main

import (
	"strings"
	"time"

	"example.com/imkit/server/host"
	"example.com/imkit/server/migrate"
	"example.com/imkit/server/migrations"
	"example.com/imkit/server/push"
)

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
	return h.host.AreFriends(client.appUserID, peerAppUserID)
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
		go h.pushE2e(toUID, topic)
	}
}

// pushE2e wakes an offline recipient's device for a queued encrypted
// envelope. The payload is deliberately content-blind — no preview, no
// sender name, nothing derived from the ciphertext or from anything the
// server would have to have decrypted to know: the server structurally
// never holds E2EE plaintext (see relayE2eEnvelope's block comment / the
// "-- E2EE --" note above handleE2eSend), and a push payload is not an
// exception to that. push.KindE2eMessage enforces it whatever else is set.
// The client shows a fixed "you have a new encrypted message" string — on
// Android see FcmService.onMessageReceived's data["e2e"]=="true" branch; on
// APNs and Web Push it is the localisation key push.LocKeyE2eMessage.
func (h *Hub) pushE2e(toUID, topic string) {
	rc := CurrentRuntimeConfig()
	if !rc.FCMEnabled || h.pusher == nil {
		return
	}
	tokensByUID, err := h.store.GetPushTokensBatch([]string{toUID})
	if err != nil {
		logf("pushE2e: GetPushTokensBatch failed: %v", err)
		return
	}
	h.sendPush(toUID, tokensByUID[toUID], push.Notification{
		Kind:    push.KindE2eMessage,
		Topic:   topic,
		IsGroup: strings.HasPrefix(topic, "grp_"),
	}, "pushE2e")
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

// e2eeModule is end-to-end encryption: the server relays opaque envelopes and
// key bundles it cannot read, and keeps plaintext out of encrypted groups.
type e2eeModule struct{}

func (e2eeModule) name() string { return "e2ee" }

func (e2eeModule) actions(h *Hub) map[string]func(*Client, *ClientMessage) {
	return map[string]func(*Client, *ClientMessage){
		ActionE2ePublishBundle:     h.handleE2ePublishBundle,
		ActionE2eFetchBundle:       h.handleE2eFetchBundle,
		ActionE2eOtpkCount:         h.handleE2eOtpkCount,
		ActionE2eSend:              h.handleE2eSend,
		ActionE2eTerminate:         h.handleE2eTerminate,
		ActionE2eClear:             h.handleE2eClear,
		ActionE2eGroupClear:        h.handleE2eGroupClear,
		ActionE2eSetGroupEncrypted: h.handleE2eSetGroupEncrypted,
	}
}

func (e2eeModule) migrations() (migrate.Set, error) { return migrations.E2ee() }

func (e2eeModule) start(h *Hub, cfg host.Config) {
	// Encrypted payloads queued for offline recipients expire; see
	// Store.StartE2eEnvelopeReaper.
	go h.store.StartE2eEnvelopeReaper()
	// Same rationale as the runtime-config refresh, separate cycle — see
	// E2eeRuntimeConfig's doc comment for why it isn't folded into that one.
	go StartE2eeConfigRefresh(cfg, runtimeConfigRefreshInterval)
}

func (e2eeModule) afterLogin(h *Hub, c *Client) { h.deliverPendingE2eEnvelopes(c) }

func (e2eeModule) beforeStore(h *Hub, c *Client, msg *ClientMessage, topic string) bool {
	// Encrypted groups must never accept a plain pub: the whole point of
	// e2e_set_group_encrypted is that from that moment on, every member's
	// client believes this topic's content is unreadable server-side, and a
	// plaintext pub landing in im_messages — client bug, stale/unencrypted
	// build, or a deliberately malicious client — would be a silent breach
	// of that belief for everyone else in the group. 1:1 encrypted threads
	// need no equivalent check here: they live under a wholly separate
	// e2e_ topic namespace that resolveP2PTopic never produces, so there is
	// no shared topic id for a plain pub to collide with in the first place.
	if strings.HasPrefix(topic, "grp_") {
		if encrypted, err := h.store.IsGroupEncrypted(topic); err == nil && encrypted {
			c.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeConflict, "group_encrypted"))
			return false
		}
	}
	return true
}

func (e2eeModule) afterStore(h *Hub, c *Client, msg *ClientMessage, topic string, seq int) bool {
	// TOCTOU close: the check in beforeStore and this write are not one atomic
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
	if strings.HasPrefix(topic, "grp_") {
		if encrypted, err := h.store.IsGroupEncrypted(topic); err == nil && encrypted {
			urls, _, delErr := h.store.SoftDeleteMessage(topic, seq, c.uid)
			if delErr != nil {
				logf("handlePub: TOCTOU self-correction failed to recall seq=%d topic=%s: %v", seq, topic, delErr)
			}
			// The plaintext message's media goes with it, as on any recall.
			for _, u := range urls {
				go h.gcMediaBlob(u)
			}
			c.SendJSON(ErrorResponse(msg.Id, ActionPub, CodeConflict, "group_encrypted"))
			return false
		}
	}
	return true
}

// memberRemoved runs for a kick ("member_removed") and for a voluntary leave
// ("member_left", the leaver excluded): a leave removes a Sender Key holder
// exactly the same way a kick does.
func (e2eeModule) memberRemoved(h *Hub, topic, by, reason, exclude string) {
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
	if encrypted, encErr := h.store.IsGroupEncrypted(topic); encErr == nil && encrypted {
		h.BroadcastToTopic(topic, &ServerMessage{
			Action: ActionE2eSetGroupEncrypted, Topic: topic, From: by,
			E2eGroupEncrypted: true, Reason: reason,
		}, exclude)
	}
}
