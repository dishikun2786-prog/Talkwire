package main

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// These exercise callState.resolve() directly, with no Hub/Store/HTTP
// wiring at all — the #1 implementation risk the plan calls out
// (IM_VOICE_VIDEO_CALL_PLAN.md §4.3: "新增 time.AfterFunc 必须在任何其它方式
// 解决该会话时都显式 Stop()，否则会有计时器泄漏后触发一次事后诸葛亮的超时判
// 定，把已经正常结束的会话错误地打成 missed") is entirely a property of this
// one type, so it can be tested in complete isolation from every network/DB
// dependency the full call flow needs.

func TestCallStateResolve_TimerStoppedOnEarlyResolution(t *testing.T) {
	var fired int32
	call := &callState{status: callStateRinging}
	// Long enough relative to the sleep below that "the cancellation won the
	// race" isn't a fluke of scheduling, short enough to keep the test fast.
	call.ringTimer = time.AfterFunc(50*time.Millisecond, func() {
		atomic.AddInt32(&fired, 1)
	})

	if !call.resolve(callStateEnded, callStateRinging) {
		t.Fatal("resolve(ended, ringing) should have succeeded from the initial ringing state")
	}
	if call.status != callStateEnded {
		t.Fatalf("status = %q, want %q", call.status, callStateEnded)
	}

	time.Sleep(150 * time.Millisecond) // well past the 50ms timer

	if atomic.LoadInt32(&fired) != 0 {
		t.Fatal("ring timer fired after the call had already been resolved by another path -- resolve() must Stop() it on every successful transition out of ringing")
	}
}

func TestCallStateResolve_RingTimeoutFiringIsItselfAResolve(t *testing.T) {
	// handleCallRingTimeout's own call shape: the timer's callback resolves
	// the call itself (ringing -> ended) rather than being pre-empted by
	// something else. Stop() on an already-fired timer, from within that
	// same timer's own callback, must be a harmless no-op -- this is the
	// "resolve() folds Stop() into every successful transition
	// unconditionally, including the one the timer firing itself performs"
	// case called out in resolve()'s doc comment.
	resolvedInsideCallback := make(chan bool, 1)
	call := &callState{status: callStateRinging}
	call.ringTimer = time.AfterFunc(10*time.Millisecond, func() {
		resolvedInsideCallback <- call.resolve(callStateEnded, callStateRinging)
	})

	select {
	case ok := <-resolvedInsideCallback:
		if !ok {
			t.Fatal("the ring-timeout callback's own resolve() call should have succeeded")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("ring timer never fired")
	}
	if call.status != callStateEnded {
		t.Fatalf("status = %q, want %q", call.status, callStateEnded)
	}
}

func TestCallStateResolve_LoserDoesNotStopTimerOrChangeStatus(t *testing.T) {
	call := &callState{status: callStateActive}
	fired := make(chan struct{}, 1)
	call.ringTimer = time.AfterFunc(20*time.Millisecond, func() { fired <- struct{}{} })

	// A stale/duplicate attempt to resolve FROM "ringing" must fail without
	// side effects: this call is active, not ringing (e.g. a duplicate
	// call_cancel arriving after the call was already answered).
	if call.resolve(callStateEnded, callStateRinging) {
		t.Fatal("resolve should have failed: call is active, not ringing")
	}
	if call.status != callStateActive {
		t.Fatalf("a failed resolve must never mutate status; got %q", call.status)
	}

	select {
	case <-fired:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timer should still fire normally -- a losing resolve() call must not stop a timer it doesn't own the outcome of")
	}
}

func TestCallStateResolve_OnlyOneOfConcurrentCallersWins(t *testing.T) {
	// The actual shape of the race resolve() exists for: e.g. call_hangup
	// arriving on the peer's own ReadPump goroutine at the same instant the
	// ring-timeout AfterFunc goroutine fires, or a disconnect processed on
	// Hub.Run's goroutine racing either. Exactly one of any number of
	// concurrent attempts to leave "ringing" must succeed.
	call := &callState{status: callStateRinging}
	const attempts = 50
	var wins int32
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if call.resolve(callStateEnded, callStateRinging) {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one concurrent resolve() call should win a CAS-style transition, got %d", wins)
	}
}

func TestCallStateResolve_MultipleFromStates(t *testing.T) {
	// handleCallHangup's own shape: valid from either "answering" (the
	// narrow post-accept, pre-call_answered window) or "active", but NOT
	// from "ringing" (that's call_cancel/call_reject's job instead -- see
	// handleCallCancel's doc comment for why a stale cancel must not
	// retroactively undo an answer already in progress).
	for _, from := range []string{callStateAnswering, callStateActive} {
		call := &callState{status: from}
		if !call.resolve(callStateEnded, callStateAnswering, callStateActive) {
			t.Errorf("resolve from %q should have succeeded against the (answering, active) set", from)
		}
	}

	call := &callState{status: callStateRinging}
	if call.resolve(callStateEnded, callStateAnswering, callStateActive) {
		t.Fatal("resolve from ringing must not match the (answering, active) set")
	}
}

// These exercise the CAS fix on callState.setActiveType(): the actual free/
// unbilled-minutes production incident this file's comment (and
// setActiveType's own doc comment) describes. Before this fix, both
// handleCallAccept and handleCallUpgradeAccept called an earlier, unconditional
// version of this method immediately after a blocking HTTP round trip
// (AnswerCallSession / UpgradeCallSession) with no re-check of whether the
// call was still in the state they left it in -- if forceEndCallForDisconnect
// (running on Hub.Run's own goroutine) won a race to resolve() the same call
// to callStateEnded while that HTTP call was in flight, the unconditional
// version would still hand out a real, unrevoked ARTC credential and push
// call_answered/call_upgraded for a call Node had already marked terminal and
// stopped billing on. A callee could trigger this deliberately (accept, then
// force a sub-3-second disconnect+reconnect) for free conversation minutes.

func TestCallStateSetActiveType_SucceedsFromMatchingState(t *testing.T) {
	call := &callState{status: callStateAnswering, callType: CallTypeVideo}
	if !call.setActiveType(CallTypeVoice, callStateAnswering) {
		t.Fatal("setActiveType should succeed: call is still in the required from-state")
	}
	if call.status != callStateActive {
		t.Fatalf("status = %q, want %q", call.status, callStateActive)
	}
	if call.callType != CallTypeVoice {
		t.Fatalf("callType = %q, want %q (AcceptAsVoice's effectiveType must actually take effect)", call.callType, CallTypeVoice)
	}
}

func TestCallStateSetActiveType_FailsAndTouchesNothingWhenRaceLost(t *testing.T) {
	// The exact production shape: forceEndCallForDisconnect's resolve() won
	// while this handler's HTTP round trip was still in flight, moving the
	// call to callStateEnded before setActiveType got a chance to run.
	call := &callState{status: callStateAnswering, callType: CallTypeVideo}
	if !call.resolve(callStateEnded, callStateAnswering) {
		t.Fatal("setup: the simulated concurrent disconnect's resolve() should have succeeded")
	}

	if call.setActiveType(CallTypeVoice, callStateAnswering) {
		t.Fatal("setActiveType must fail once the call has already left the required from-state -- this is the exact gap that let a disconnect-racing accept still hand out a live ARTC credential for a call Node had already settled")
	}
	if call.status != callStateEnded {
		t.Fatalf("a failed setActiveType must never mutate status; got %q, want unchanged %q", call.status, callStateEnded)
	}
	if call.callType != CallTypeVideo {
		t.Fatalf("a failed setActiveType must never mutate callType; got %q, want unchanged %q", call.callType, CallTypeVideo)
	}
}

func TestCallStateSetActiveType_OnlyOneOfConcurrentCallersWins(t *testing.T) {
	// Mirrors TestCallStateResolve_OnlyOneOfConcurrentCallersWins for the
	// same CAS contract on setActiveType: handleCallAccept and
	// forceEndCallForDisconnect can race on genuinely different goroutines
	// (a real network call blocking one of them is exactly what creates the
	// window), and exactly one outcome may win.
	const attempts = 50
	var wins int32
	var wg sync.WaitGroup
	wg.Add(attempts)
	call := &callState{status: callStateAnswering, callType: CallTypeVoice}
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if call.setActiveType(CallTypeVideo, callStateAnswering) {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one concurrent setActiveType() call should win the CAS transition, got %d", wins)
	}
	if call.status != callStateActive {
		t.Fatalf("the single winner should have moved status to %q, got %q", callStateActive, call.status)
	}
}

func TestCallHub_ReserveRejectsWhenEitherParticipantBusy(t *testing.T) {
	ch := newCallHub()
	first := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging}
	if !ch.reserve(first) {
		t.Fatal("first reservation on two free uids should succeed")
	}

	// Same callee, different caller -- callee is busy.
	second := &callState{id: "s2", callerUID: "uid_c", calleeUID: "uid_b", status: callStateRinging}
	if ch.reserve(second) {
		t.Fatal("reserve should fail: uid_b is already in a call")
	}

	// Same caller, different callee -- caller is busy (im-go's own "one
	// call per uid" invariant, plan §9.3 -- not a distinct wire reason from
	// the callee-busy case, see handleCallInvite's own comment on this).
	third := &callState{id: "s3", callerUID: "uid_a", calleeUID: "uid_d", status: callStateRinging}
	if ch.reserve(third) {
		t.Fatal("reserve should fail: uid_a is already in a call")
	}

	if got := ch.getByUID("uid_a"); got != first {
		t.Fatal("uid_a should still resolve to the original call, not a partially-installed second one")
	}
	if got := ch.bySession["s2"]; got != nil {
		t.Fatal("a rejected reserve() must not have partially installed itself under bySession")
	}
}

// These exercise scheduleForceEndCallForDisconnect / cancelPendingCallDisconnect
// directly on a bare *Hub (no real WebSocket/Store/AuthService/InternalAPI
// wiring -- settleCallAsync's own recover() makes a nil h.callHost safe,
// same reasoning as the callState-only tests above). This is the actual fix
// for "接通后立刻挂断" on VIVO: forceEndCallForDisconnect used to run
// instantly on any real disconnect, with zero allowance for the reconnect
// that OEM background-management teardown/rebuild produces a few seconds
// later -- see scheduleForceEndCallForDisconnect's own doc comment.

func newTestHubForCallDisconnect() *Hub {
	return &Hub{clients: make(map[string]*Client), calls: newCallHub()}
}

func withShortCallDisconnectGrace(d time.Duration, fn func()) {
	prev := callDisconnectGraceDuration
	callDisconnectGraceDuration = d
	defer func() { callDisconnectGraceDuration = prev }()
	fn()
}

func TestScheduleForceEndCallForDisconnect_NoOpWhenNoCallInFlight(t *testing.T) {
	h := newTestHubForCallDisconnect()
	// uid_a has no call reserved at all -- the overwhelmingly common shape
	// of any disconnect (logout, app close, network blip with nothing to
	// protect). Must not leave a timer behind to leak or ever fire.
	h.scheduleForceEndCallForDisconnect("uid_a")
	if _, ok := h.pendingCallDisconnects.Load("uid_a"); ok {
		t.Fatal("no call was in flight for uid_a -- scheduleForceEndCallForDisconnect must not have scheduled anything")
	}
}

func TestScheduleForceEndCallForDisconnect_EndsCallAfterGraceWithNoReconnect(t *testing.T) {
	withShortCallDisconnectGrace(30*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		call := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging}
		if !h.calls.reserve(call) {
			t.Fatal("setup: reserve should have succeeded on two free uids")
		}

		h.scheduleForceEndCallForDisconnect("uid_b")

		// Immediately after scheduling, the call must still be alive -- this
		// is the entire point of the fix: no instant force-end.
		if h.calls.getByUID("uid_b") == nil {
			t.Fatal("call was force-ended immediately instead of waiting out the grace period")
		}

		time.Sleep(150 * time.Millisecond) // well past the 30ms grace period

		if h.calls.getByUID("uid_b") != nil {
			t.Fatal("call should have been force-ended once the grace period elapsed with no reconnect")
		}
		if call.status != callStateEnded {
			t.Fatalf("status = %q, want %q", call.status, callStateEnded)
		}
		if _, ok := h.pendingCallDisconnects.Load("uid_b"); ok {
			t.Fatal("the fired timer should have removed its own pendingCallDisconnects entry")
		}
	})
}

func TestScheduleForceEndCallForDisconnect_ReconnectWithinGraceSurvivesTheCall(t *testing.T) {
	// The actual production shape this fixes: VIVO backgrounds the app mid-
	// ring, the watchdog tears down and rebuilds the socket, and the
	// reconnect's register lands a real amount of time later -- but well
	// inside the grace period -- while the call is still ringing/answering.
	withShortCallDisconnectGrace(200*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		call := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging}
		if !h.calls.reserve(call) {
			t.Fatal("setup: reserve should have succeeded on two free uids")
		}

		h.scheduleForceEndCallForDisconnect("uid_b")

		time.Sleep(50 * time.Millisecond) // comfortably inside the 200ms grace period
		h.cancelPendingCallDisconnect("uid_b")

		time.Sleep(300 * time.Millisecond) // well past where the original grace period would have fired

		if h.calls.getByUID("uid_b") == nil {
			t.Fatal("the reconnect landed within the grace period -- the call must have survived, not been force-ended")
		}
		if call.status != callStateRinging {
			t.Fatalf("status = %q, want unchanged %q -- a cancelled grace timer must not touch the call at all", call.status, callStateRinging)
		}
	})
}

func TestScheduleForceEndCallForDisconnect_LiveReconnectCaughtEvenIfCancelMissed(t *testing.T) {
	// Defense in depth: even if cancelPendingCallDisconnect somehow never
	// ran (the register branch is skipped/loses a race), the fired timer's
	// own GetClientByUID re-check must still save a call whose uid is
	// demonstrably live again.
	withShortCallDisconnectGrace(30*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		call := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging}
		if !h.calls.reserve(call) {
			t.Fatal("setup: reserve should have succeeded on two free uids")
		}

		h.scheduleForceEndCallForDisconnect("uid_b")

		// Simulate the reconnect's register having landed in h.clients
		// directly, WITHOUT going through cancelPendingCallDisconnect --
		// exactly the "missed the cancel" scenario the timer's own re-check
		// exists to catch.
		h.mu.Lock()
		h.clients["uid_b"] = &Client{uid: "uid_b"}
		h.mu.Unlock()

		time.Sleep(150 * time.Millisecond) // well past the 30ms grace period

		if h.calls.getByUID("uid_b") == nil {
			t.Fatal("uid_b was live in h.clients when the timer fired -- the call must have been spared even without an explicit cancel")
		}
	})
}

func TestScheduleForceEndCallForDisconnect_FlappingDisconnectResetsTheFullWindow(t *testing.T) {
	// Two disconnects for the same uid before the first grace period would
	// have fired must not let the call end on the FIRST (shorter) window --
	// the second call replaces the timer with a fresh full-length one.
	withShortCallDisconnectGrace(120*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		call := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging}
		if !h.calls.reserve(call) {
			t.Fatal("setup: reserve should have succeeded on two free uids")
		}

		h.scheduleForceEndCallForDisconnect("uid_b")
		time.Sleep(80 * time.Millisecond) // inside the first window, but close to it
		h.scheduleForceEndCallForDisconnect("uid_b")

		time.Sleep(80 * time.Millisecond) // now 160ms after the first schedule -- past its
		// original 120ms deadline, but only 80ms after the second (replacement)
		// schedule, so still comfortably inside ITS 120ms window.
		if h.calls.getByUID("uid_b") == nil {
			t.Fatal("the second disconnect should have replaced the timer with a fresh full window -- the call ended on the stale first window instead")
		}

		time.Sleep(100 * time.Millisecond) // now well past the second window too
		if h.calls.getByUID("uid_b") != nil {
			t.Fatal("the replacement timer should still fire normally once ITS grace period elapses")
		}
	})
}

func TestCancelPendingCallDisconnect_NoOpWhenNothingPending(t *testing.T) {
	h := newTestHubForCallDisconnect()
	// Must not panic on a bare LoadAndDelete miss -- the overwhelming
	// majority of registers (any fresh login, any uid never in a call).
	h.cancelPendingCallDisconnect("uid_never_scheduled")
}

func TestCallHub_RemoveIsIdentitySafe(t *testing.T) {
	// Same "identity, not string key" discipline as Hub.Run's own topic
	// cleanup (see callHub.remove's doc comment): removing a stale
	// *callState must never evict whatever call now legitimately occupies
	// that uid/session slot.
	ch := newCallHub()
	stale := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateEnded}
	ch.reserve(stale)
	ch.remove(stale)

	fresh := &callState{id: "s2", callerUID: "uid_a", calleeUID: "uid_c", status: callStateRinging}
	if !ch.reserve(fresh) {
		t.Fatal("uid_a should be free again after the stale call was removed")
	}

	// Removing the ALREADY-removed stale call again (e.g. a duplicate
	// cleanup call racing itself) must be a no-op, not evict `fresh`.
	ch.remove(stale)
	if got := ch.getByUID("uid_a"); got != fresh {
		t.Fatal("removing a stale call a second time must not have evicted the call that now legitimately owns uid_a")
	}
	if got := ch.get("s2"); got != fresh {
		t.Fatal("bySession[s2] should still resolve to fresh")
	}
}

// The call module through the registry: its hooks run only when it is
// installed, and a call the host ended frees both participants' slot.

func reservedCall(h *Hub) *callState {
	call := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateActive}
	h.calls.reserve(call)
	return call
}

func TestCallModule_HostEndedCallReleasesTheSlot(t *testing.T) {
	h := newTestHubForCallDisconnect()
	if _, err := installModules(h, nopHost{}, []string{"call"}, true); err != nil {
		t.Fatal(err)
	}
	reservedCall(h)
	h.mods.hostBroadcast(h, "uid_a", "credit_balance")
	if h.calls.getByUID("uid_a") == nil {
		t.Fatal("a payload other than call_ended released the call")
	}
	h.mods.hostBroadcast(h, "uid_b", "call_ended")
	if h.calls.getByUID("uid_a") != nil || h.calls.getByUID("uid_b") != nil {
		t.Fatal("call_ended from the host left the slot taken")
	}
}

func TestCallModule_ReconnectCancelsTheGraceTimer(t *testing.T) {
	withShortCallDisconnectGrace(50*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		if _, err := installModules(h, nopHost{}, []string{"call"}, true); err != nil {
			t.Fatal(err)
		}
		call := reservedCall(h)
		h.mods.disconnected(h, "uid_a")
		h.mods.connected(h, "uid_a")
		time.Sleep(150 * time.Millisecond)
		if _, status := call.currentTypeAndStatus(); status != callStateActive {
			t.Fatalf("call ended (%s) although the participant reconnected within the grace period", status)
		}
	})
}

func TestCallModule_NotInstalledLeavesCallsAlone(t *testing.T) {
	withShortCallDisconnectGrace(20*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		if _, err := installModules(h, nopHost{}, nil, true); err != nil {
			t.Fatal(err)
		}
		call := reservedCall(h)
		h.mods.disconnected(h, "uid_a")
		h.mods.hostBroadcast(h, "uid_a", "call_ended")
		time.Sleep(80 * time.Millisecond)
		if _, status := call.currentTypeAndStatus(); status != callStateActive {
			t.Fatal("hooks of an uninstalled call module ran")
		}
	})
}
