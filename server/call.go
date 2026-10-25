// Voice and video call signalling (optional module): call state machine, ring timeout, disconnect grace.

package main

import (
	"sync"
	"time"
)

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
