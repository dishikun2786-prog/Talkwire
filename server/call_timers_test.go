package main

import (
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeCallClock stands in for time.AfterFunc in the call module (callAfterFunc):
// a timer fires only when the test moves the clock past it, so the grace
// period is checked at its real length without waiting.
type fakeCallClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeCallTimer
}

type fakeCallTimer struct {
	clock   *fakeCallClock
	at      time.Duration
	f       func()
	fired   bool
	stopped bool
}

func (t *fakeCallTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	live := !t.fired && !t.stopped
	t.stopped = true
	return live
}

func useFakeCallClock(t *testing.T) *fakeCallClock {
	t.Helper()
	c := &fakeCallClock{}
	prev := callAfterFunc
	callAfterFunc = func(d time.Duration, f func()) callTimer {
		c.mu.Lock()
		defer c.mu.Unlock()
		tm := &fakeCallTimer{clock: c, at: c.now + d, f: f}
		c.timers = append(c.timers, tm)
		return tm
	}
	t.Cleanup(func() { callAfterFunc = prev })
	return c
}

// advance moves the clock forward and runs every timer that came due, earliest first.
func (c *fakeCallClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []*fakeCallTimer
	for _, tm := range c.timers {
		if !tm.fired && !tm.stopped && tm.at <= c.now {
			tm.fired = true
			due = append(due, tm)
		}
	}
	c.mu.Unlock()
	sort.SliceStable(due, func(i, j int) bool { return due[i].at < due[j].at })
	for _, tm := range due {
		tm.f()
	}
}

// live counts the timers that are neither stopped nor fired.
func (c *fakeCallClock) live() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, tm := range c.timers {
		if !tm.fired && !tm.stopped {
			n++
		}
	}
	return n
}

// callFixture is one call between uid_a and uid_b with the call module
// installed, a host that counts settlements, and the injected clock.
type callFixture struct {
	t     *testing.T
	h     *Hub
	host  *countingCallHost
	clock *fakeCallClock
	call  *callState
}

func newCallFixture(t *testing.T, status string) *callFixture {
	t.Helper()
	clock := useFakeCallClock(t)
	h := newTestHubForCallDisconnect()
	callHost := &countingCallHost{settled: map[string][]string{}}
	if _, err := installModules(h, callHost, []string{"call"}, true); err != nil {
		t.Fatal(err)
	}
	call := &callState{id: "r16", callerUID: "uid_a", calleeUID: "uid_b", status: status, callType: CallTypeVoice}
	if !h.calls.reserve(call) {
		t.Fatal("setup: reserve failed")
	}
	return &callFixture{t: t, h: h, host: callHost, clock: clock, call: call}
}

// online connects uid on the default device and returns its connection.
func (f *callFixture) online(uid string) *Client {
	c := sideClient(f.h, uid, "app-"+uid)
	f.h.mu.Lock()
	f.h.clients[uid] = map[string]*Client{"": c}
	f.h.mu.Unlock()
	f.h.mods.connected(f.h, uid, "")
	return c
}

// offline drops uid's connection the way Hub.Run does, which starts its grace period.
func (f *callFixture) offline(uid string) {
	f.h.mu.Lock()
	delete(f.h.clients, uid)
	f.h.mu.Unlock()
	f.h.mods.disconnected(f.h, uid, "")
}

func (f *callFixture) status() string {
	_, s := f.call.currentTypeAndStatus()
	return s
}

func (f *callFixture) pending(uid string) bool {
	_, ok := f.h.pendingCallDisconnects.Load(uid)
	return ok
}

// settledAs waits for the host's settlement of the call and requires exactly one, for reason.
func (f *callFixture) settledAs(reason string) {
	f.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); len(f.host.settlements()[f.call.id]) == 0 && time.Now().Before(deadline); {
		time.Sleep(2 * time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // room for a second settlement to show up
	if got := f.host.settlements()[f.call.id]; len(got) != 1 || got[0] != reason {
		f.t.Fatalf("settlements %v, want exactly [%s]", got, reason)
	}
}

// R16: with the clock injected, a participant who stays away longer than the
// 10-second grace period ends the call (the other side hears "disconnected",
// the host settles it once); one who comes back within it keeps the call and
// leaves no timer behind.
func TestR16_DisconnectGraceEndsCall(t *testing.T) {
	if os.Getenv("IM_CALL_DISCONNECT_GRACE_SEC") == "" && callDisconnectGraceDuration != 10*time.Second {
		t.Fatalf("default grace period %v, want 10s", callDisconnectGraceDuration)
	}
	grace := callDisconnectGraceDuration

	t.Run("away longer than the grace period", func(t *testing.T) {
		f := newCallFixture(t, callStateActive)
		b := f.online("uid_b")
		f.offline("uid_a")
		f.clock.advance(grace - time.Millisecond)
		if f.status() != callStateActive || !f.pending("uid_a") {
			t.Fatalf("call ended before the grace period ran out: %s", f.status())
		}
		f.clock.advance(time.Millisecond)
		if f.status() != callStateEnded {
			t.Fatalf("status %s once the grace period ran out", f.status())
		}
		if ended := nextFrame(t, b); ended.str("type") != ActionCallEnded || ended.str("reason") != "disconnected" {
			t.Fatalf("the other side got %v", ended)
		}
		f.settledAs("disconnected")
		if f.h.calls.getByUID("uid_a") != nil || f.h.calls.getByUID("uid_b") != nil || f.pending("uid_a") || f.clock.live() != 0 {
			t.Fatal("the ended call left a slot or a timer behind")
		}
	})

	t.Run("back within the grace period", func(t *testing.T) {
		f := newCallFixture(t, callStateActive)
		b := f.online("uid_b")
		f.offline("uid_a")
		f.clock.advance(grace / 2)
		f.online("uid_a")
		if f.pending("uid_a") || f.clock.live() != 0 {
			t.Fatal("reconnecting did not stop the grace timer")
		}
		f.clock.advance(10 * grace)
		if f.status() != callStateActive {
			t.Fatalf("status %s after reconnecting within the grace period", f.status())
		}
		if len(b.send) != 0 || len(f.host.settlements()) != 0 {
			t.Fatal("a call kept alive was ended or settled")
		}
	})

	t.Run("a later call of the same user is not this timer's", func(t *testing.T) {
		f := newCallFixture(t, callStateActive)
		f.online("uid_b")
		f.offline("uid_a")
		// The guarded call ends without removeCall (the window where a timer
		// could still fire), and uid_a is already in the next call.
		f.call.resolve(callStateEnded, callStateActive)
		f.h.calls.remove(f.call)
		next := &callState{id: "r16-next", callerUID: "uid_a", calleeUID: "uid_c", status: callStateActive, callType: CallTypeVoice}
		if !f.h.calls.reserve(next) {
			t.Fatal("setup: reserve failed")
		}
		f.clock.advance(grace)
		if _, s := next.currentTypeAndStatus(); s != callStateActive || f.h.calls.getByUID("uid_a") != next {
			t.Fatalf("the earlier call's grace timer ended the next call (%s)", s)
		}
	})
}

// R16: every way a call ends other than its own timer running out stops both
// the ring timer and a disconnect grace timer still pending for the call; and
// when the call ends through one participant's grace period, the other
// participant's pending timer stops too. Nothing fires afterwards and the host
// settles once.
func TestR16_TimersStoppedOnEveryPath(t *testing.T) {
	const ring = 45 * time.Second
	ringing := func(f *callFixture) {
		if !f.call.startRingTimer(ring, func() { f.h.handleCallRingTimeout(f.call) }) {
			f.t.Fatal("setup: ring timer not armed")
		}
	}
	msg := func(action string, f *callFixture) *ClientMessage {
		return &ClientMessage{Id: action, Action: action, CallSessionId: f.call.id}
	}
	paths := []struct {
		name   string
		status string
		reason string
		run    func(f *callFixture)
	}{
		{"caller cancels while the offline callee's grace runs", callStateRinging, "cancelled", func(f *callFixture) {
			ringing(f)
			a := f.online("uid_a")
			f.offline("uid_b")
			f.h.handleCallCancel(a, msg(ActionCallCancel, f))
		}},
		{"callee rejects while the offline caller's grace runs", callStateRinging, "rejected", func(f *callFixture) {
			ringing(f)
			b := f.online("uid_b")
			f.offline("uid_a")
			f.h.handleCallReject(b, msg(ActionCallReject, f))
		}},
		{"callee answers, then hangs up while the offline caller's grace runs", callStateRinging, "user_hangup", func(f *callFixture) {
			ringing(f)
			f.online("uid_a")
			b := f.online("uid_b")
			f.h.handleCallAccept(b, msg(ActionCallAccept, f))
			if f.status() != callStateActive {
				f.t.Fatalf("setup: status %s after answering", f.status())
			}
			f.offline("uid_a")
			f.h.handleCallHangup(b, msg(ActionCallHangup, f))
		}},
		{"the host ends the call while the caller's grace runs", callStateActive, "", func(f *callFixture) {
			f.online("uid_b")
			f.offline("uid_a")
			f.h.mods.hostBroadcast(f.h, "uid_b", "call_ended")
		}},
		{"the ring timeout ends the call while the callee's grace runs", callStateRinging, "no_answer", func(f *callFixture) {
			ringing(f)
			f.online("uid_a")
			f.online("uid_b")
			// The callee drops shortly before the ring timeout, so it fires
			// while the callee's grace period still runs.
			f.clock.advance(ring - callDisconnectGraceDuration/2)
			f.offline("uid_b")
			f.clock.advance(callDisconnectGraceDuration / 2)
		}},
		{"the caller's grace runs out first; the callee's pending grace stops", callStateActive, "disconnected", func(f *callFixture) {
			f.offline("uid_a")
			f.clock.advance(callDisconnectGraceDuration / 2)
			f.offline("uid_b")
			f.clock.advance(callDisconnectGraceDuration / 2)
		}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			f := newCallFixture(t, p.status)
			f.t = t
			p.run(f)
			if f.status() != callStateEnded {
				t.Fatalf("status %s", f.status())
			}
			if f.clock.live() != 0 || f.pending("uid_a") || f.pending("uid_b") {
				t.Fatalf("%d timer(s) still running after the call ended", f.clock.live())
			}
			if f.h.calls.getByUID("uid_a") != nil || f.h.calls.getByUID("uid_b") != nil {
				t.Fatal("the ended call still holds a slot")
			}
			f.clock.advance(time.Hour)
			if p.reason != "" {
				f.settledAs(p.reason)
			} else if got := f.host.settlements()[f.call.id]; len(got) != 0 {
				t.Fatalf("the host ended this call itself, yet the module settled it: %v", got)
			}
		})
	}
}
