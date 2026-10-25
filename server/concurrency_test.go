package main

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"example.com/imkit/server/host"
)

// Concurrency red lines (task 1.11). Each runs 100 rounds or more and is
// meant to be run under the race detector (tools/race.sh).

// sideClient is a logged-in connection with no socket: its frames stay in its
// send channel. Tests use it to drive handlers from many goroutines at once.
func sideClient(h *Hub, uid, appUser string) *Client {
	c := &Client{hub: h, send: make(chan []byte, 256), subscribed: map[string]bool{}}
	c.setUID(uid)
	c.setAppUserID(appUser)
	return c
}

// nextFrame takes the next frame a side client was sent.
func nextFrame(t *testing.T, c *Client) frame {
	t.Helper()
	select {
	case data := <-c.send:
		var f frame
		if err := json.Unmarshal(data, &f); err != nil {
			t.Fatal(err)
		}
		return f
	case <-time.After(5 * time.Second):
		t.Fatal("no frame")
		return nil
	}
}

// R1: the same clientMsgId published from several connections at once is
// stored once and broadcast once, and every publisher gets the same seq.
func TestR1_ConcurrentDuplicatePublish(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	b := ls.connect("r1-b")
	const rounds, publishers = 100, 8
	for r := 0; r < rounds; r++ {
		appA := fmt.Sprintf("r1-a-%d", r)
		uidA, err := ls.store.EnsureUser(appA)
		if err != nil {
			t.Fatal(err)
		}
		if reply := b.request(frame{"action": ActionSub, "topic": uidA}); reply.num("code") != CodeOK {
			t.Fatalf("round %d: sub: %v", r, reply)
		}
		cmid := fmt.Sprintf("dup-%d", r)
		pub := func(c *Client, clientMsgID string) {
			ls.hub.ProcessMessage(c, &ClientMessage{Id: "p", Action: ActionPub, Topic: b.uid,
				Content: "hello", ContentType: "text/plain", ClientMsgId: clientMsgID})
		}

		clients := make([]*Client, publishers)
		for i := range clients {
			clients[i] = sideClient(ls.hub, uidA, appA)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		for _, c := range clients {
			wg.Add(1)
			go func(c *Client) {
				defer wg.Done()
				<-start
				pub(c, cmid)
			}(c)
		}
		close(start)
		wg.Wait()

		var seq int64
		for i, c := range clients {
			ack := nextFrame(t, c)
			if ack.num("code") != CodeOK {
				t.Fatalf("round %d: publisher %d: %v", r, i, ack)
			}
			if i == 0 {
				seq = ack.num("seq")
			} else if ack.num("seq") != seq {
				t.Fatalf("round %d: publishers got seqs %d and %d for one message", r, seq, ack.num("seq"))
			}
		}

		// A later message is broadcast after any duplicate would have been:
		// once B has it, B has everything the duplicates could produce.
		pub(sideClient(ls.hub, uidA, appA), cmid+"-end")
		if _, ok := b.waitFor(dataWith("clientMsgId", cmid+"-end"), 5*time.Second); !ok {
			t.Fatalf("round %d: B did not receive the closing message", r)
		}
		if n := len(b.frames(dataWith("clientMsgId", cmid))); n != 1 {
			t.Fatalf("round %d: B received the message %d times", r, n)
		}
		var rows int
		if err := ls.store.db.QueryRow(`SELECT count(*) FROM im_messages WHERE client_msg_id = $1`, cmid).Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if rows != 1 {
			t.Fatalf("round %d: %d rows stored for one clientMsgId", r, rows)
		}
	}
}

// R3: one user's connections replacing each other as fast as they can. The
// connection left standing keeps every subscription it made, the user's
// friends never see them go offline, and the hub does not wedge.
func TestR3_SupersedeStorm(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	o := ls.connect("r3-o")
	uidX, err := ls.store.EnsureUser("r3-x")
	if err != nil {
		t.Fatal(err)
	}
	group, err := ls.store.CreateGroup(o.uid, "storm", []string{uidX})
	if err != nil {
		t.Fatal(err)
	}
	for _, topic := range []string{uidX, group} {
		if reply := o.request(frame{"action": ActionSub, "topic": topic}); reply.num("code") != CodeOK {
			t.Fatalf("sub %s: %v", topic, reply)
		}
	}
	before := len(o.frames(presOf(uidX)))

	const rounds, perRound = 100, 3
	var conns []*liveConn
	for r := 0; r < rounds; r++ {
		round := make([]*liveConn, perRound)
		var wg sync.WaitGroup
		for i := range round {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				c, _, err := ls.dial("/ws")
				if err != nil {
					t.Error(err)
					return
				}
				// Pipelined: the subs are still in flight when the next
				// login replaces this connection.
				c.send(frame{"action": ActionLogin, "token": accessToken("r3-x")})
				c.send(frame{"action": ActionSub, "id": "s1", "topic": o.uid})
				c.send(frame{"action": ActionSub, "id": "s2", "topic": group})
				round[i] = c
			}(i)
		}
		wg.Wait()
		if t.Failed() {
			t.FailNow()
		}
		conns = append(conns, round...)
	}

	var survivor *liveConn
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var open []*liveConn
		for _, c := range conns {
			if !c.closed() {
				open = append(open, c)
			}
		}
		if len(open) == 1 {
			survivor = open[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of the user's connections are still open", len(open))
		}
	}
	for _, id := range []string{"s1", "s2"} {
		reply, ok := survivor.waitFor(func(f frame) bool { return f.str("id") == id }, 5*time.Second)
		if !ok || reply.num("code") != CodeOK {
			t.Fatalf("survivor's sub %s: %v", id, reply)
		}
	}

	for _, m := range []struct{ topic, content string }{{uidX, "p2p after storm"}, {group, "group after storm"}} {
		if reply := o.request(frame{"action": ActionPub, "topic": m.topic, "content": m.content, "contentType": "text/plain"}); reply.num("code") != CodeOK {
			t.Fatalf("pub: %v", reply)
		}
		if _, ok := survivor.waitFor(dataWith("content", m.content), 5*time.Second); !ok {
			t.Fatalf("the surviving connection lost its subscription: %q never arrived", m.content)
		}
	}

	// Closing the survivor is the one real offline; once O has that push,
	// O has every presence push of the storm.
	var lastRev int64
	for _, f := range o.frames(presOf(uidX)) {
		if f.num("presRev") > lastRev {
			lastRev = f.num("presRev")
		}
	}
	survivor.close()
	if _, ok := o.waitFor(func(f frame) bool {
		return presOf(uidX)(f) && f.str("what") == PresOffline && f.num("presRev") > lastRev
	}, 5*time.Second); !ok {
		t.Fatal("no offline after the last connection closed")
	}
	pushes := o.frames(presOf(uidX))[before:]
	for i, f := range pushes[:len(pushes)-1] {
		if f.str("what") == PresOffline {
			t.Fatalf("push %d of %d: the user was shown offline while connections were replacing each other", i+1, len(pushes))
		}
	}
	assertRevsIncrease(t, uidX, pushes)
}

func assertRevsIncrease(t *testing.T, uid string, pushes []frame) {
	t.Helper()
	for i := 1; i < len(pushes); i++ {
		if pushes[i].num("presRev") <= pushes[i-1].num("presRev") {
			t.Fatalf("%s: presRev %d followed %d (push %d of %d)", uid, pushes[i].num("presRev"), pushes[i-1].num("presRev"), i+1, len(pushes))
		}
	}
}

// R4: many users connecting, replacing and closing connections at once.
// Each user's revisions only increase, and once things settle the last state
// pushed and the snapshot both match whether the user has a connection.
func TestR4_PresenceRevMonotonic(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	o := ls.connect("r4-o")
	const users, cycles = 5, 40
	uids := make([]string, users)
	before := make([]int, users)
	for i := range uids {
		uid, err := ls.store.EnsureUser(fmt.Sprintf("r4-u%d", i))
		if err != nil {
			t.Fatal(err)
		}
		uids[i] = uid
		if reply := o.request(frame{"action": ActionSub, "topic": uid}); reply.num("code") != CodeOK {
			t.Fatalf("sub: %v", reply)
		}
		before[i] = len(o.frames(presOf(uid)))
	}

	conns := make([][]*liveConn, users)
	var wg sync.WaitGroup
	for i := 0; i < users; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(i)))
			app := fmt.Sprintf("r4-u%d", i)
			var prev *liveConn
			for k := 0; k < cycles; k++ {
				c, _, err := ls.dial("/ws")
				if err != nil {
					t.Error(err)
					return
				}
				c.send(frame{"action": ActionLogin, "token": accessToken(app)})
				conns[i] = append(conns[i], c)
				switch rng.Intn(3) {
				case 0: // leave the previous one for the new login to replace
				case 1:
					if prev != nil {
						prev.close()
					}
				case 2:
					if prev != nil {
						prev.close()
					}
					time.Sleep(time.Duration(rng.Intn(3)) * time.Millisecond)
				}
				prev = c
			}
			if i%2 == 1 {
				prev.close() // half the users end offline
			}
		}(i)
	}
	wg.Wait()
	if t.Failed() {
		t.FailNow()
	}

	settled := func() (string, bool) {
		o.send(frame{"action": ActionPres, "topics": uids})
		snap, ok := o.waitFor(func(f frame) bool { return f.str("what") == PresSnapshot }, 5*time.Second)
		if !ok {
			return "no snapshot reply", false
		}
		states := map[string]frame{}
		for _, s := range snap["states"].([]any) {
			st := frame(s.(map[string]any))
			states[st.str("uid")] = st
		}
		for i, uid := range uids {
			open := 0
			for _, c := range conns[i] {
				if !c.closed() {
					open++
				}
			}
			if open > 1 {
				return fmt.Sprintf("%s has %d open connections", uid, open), false
			}
			pushes := o.frames(presOf(uid))[before[i]:]
			if len(pushes) == 0 {
				return fmt.Sprintf("%s: no pushes", uid), false
			}
			last := pushes[len(pushes)-1]
			if online := last.str("what") == PresOnline; online != (open == 1) {
				return fmt.Sprintf("%s: last push %s, open connections %d", uid, last.str("what"), open), false
			}
			st := states[uid]
			if st["online"] != (open == 1) || st.num("presRev") != last.num("presRev") {
				return fmt.Sprintf("%s: snapshot %v, last push %v", uid, st, last), false
			}
		}
		return "", true
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		why, ok := settled()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(why)
		}
	}
	for i, uid := range uids {
		assertRevsIncrease(t, uid, o.frames(presOf(uid))[before[i]:])
	}
}

// R10: connections over the cap are refused before the upgrade; the open
// ones keep working, and a closed one gives its slot back.
func TestR10_ConnectionCap(t *testing.T) {
	ls := newLiveServer(t, liveOptions{connCap: 3})
	a := ls.connect("r10-a")
	b := ls.connect("r10-b")
	c := ls.connect("r10-c")
	for i := 0; i < 20; i++ {
		if extra, resp, err := ls.dial("/ws"); err == nil {
			extra.close()
			t.Fatal("a connection over the cap was accepted")
		} else if resp == nil || resp.StatusCode != 503 {
			t.Fatalf("over the cap: %v, %v", resp, err)
		}
	}

	if reply := a.request(frame{"action": ActionSub, "topic": b.uid}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	if reply := a.request(frame{"action": ActionPub, "topic": b.uid, "content": "still here", "contentType": "text/plain"}); reply.num("code") != CodeOK {
		t.Fatalf("pub: %v", reply)
	}
	if _, ok := b.waitFor(dataWith("content", "still here"), 5*time.Second); !ok {
		t.Fatal("an open connection stopped receiving at the cap")
	}

	c.close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		d, _, err := ls.dial("/ws")
		if err == nil {
			if f := d.login("r10-d"); f.num("code") != CodeOK {
				t.Fatalf("login after a slot was freed: %v", f)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("closing a connection did not free its slot: %v", err)
		}
	}
}

// R10: a connection that stops draining its frames is closed when its queue
// fills; nobody else waits for it.
func TestR10_SendQueueFullDisconnects(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	fast := ls.connect("r10q-fast")
	stalled := ls.connectAt("/ws-stalled", "r10q-slow")
	uidSlow, err := ls.store.EnsureUser("r10q-slow")
	if err != nil {
		t.Fatal(err)
	}
	producer, err := ls.store.EnsureUser("r10q-producer")
	if err != nil {
		t.Fatal(err)
	}
	group, err := ls.store.CreateGroup(producer, "queue", []string{fast.uid, uidSlow})
	if err != nil {
		t.Fatal(err)
	}
	if reply := fast.request(frame{"action": ActionSub, "topic": group}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	stalled.send(frame{"action": ActionSub, "id": "s", "topic": group})
	var slow *Client
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if slow = ls.hub.clientOf(uidSlow, ""); slow != nil && ls.hub.isSubscribedInIndex(group, slow) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the stalled connection never subscribed")
		}
	}

	const total, batch = 400, 50
	for i := 0; i < total; i++ {
		content := fmt.Sprintf("m%d", i)
		if _, _, _, err := ls.hub.deliverMessage(group, producer, SenderKindUser, content, "text/plain", "", "", nil, 0, nil, "", nil); err != nil {
			t.Fatal(err)
		}
		if i%batch == batch-1 {
			if _, ok := fast.waitFor(dataWith("content", content), 5*time.Second); !ok {
				t.Fatalf("the fast connection fell behind at %s", content)
			}
		}
	}
	if !stalled.waitClosed(5 * time.Second) {
		t.Fatal("the stalled connection was not closed when its queue filled")
	}
	for deadline := time.Now().Add(5 * time.Second); ls.hub.clientOf(uidSlow, "") != nil; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the stalled connection is still registered")
		}
	}
	got := fast.frames(func(f frame) bool { return f.str("action") == ActionData })
	if len(got) != total {
		t.Fatalf("the fast connection received %d of %d messages", len(got), total)
	}
	for i, f := range got {
		if f.str("content") != fmt.Sprintf("m%d", i) {
			t.Fatalf("message %d is %q", i, f.str("content"))
		}
	}
	if fast.closed() {
		t.Fatal("the fast connection was closed")
	}
}

// countingCallHost answers every call and records each settlement.
type countingCallHost struct {
	nopHost
	mu      sync.Mutex
	settled map[string][]string
}

func (c *countingCallHost) AnswerCallSession(string, string) (host.AnswerCallSessionResult, error) {
	// The real host answers over HTTP; vary how long the call sits in
	// "answering".
	time.Sleep(time.Duration(rand.Intn(300)) * time.Microsecond)
	return host.AnswerCallSessionResult{Ok: true}, nil
}

func (c *countingCallHost) SettleCallSession(id, reason string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settled[id] = append(c.settled[id], reason)
	return nil
}

func (c *countingCallHost) settlements() map[string][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]string, len(c.settled))
	for k, v := range c.settled {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// R16: accept, hangup, cancel, reject, the ring timeout and a disconnect, all
// on one call at the same moment. The call ends exactly once and the host
// settles it exactly once, for a reason consistent with whether the accept
// went through.
func TestR16_CallStateCASUnderRace(t *testing.T) {
	h := newTestHubForCallDisconnect()
	callHost := &countingCallHost{settled: map[string][]string{}}
	if _, err := installModules(h, callHost, []string{"call"}, true); err != nil {
		t.Fatal(err)
	}
	const rounds = 200
	accepted := map[string]bool{}
	for r := 0; r < rounds; r++ {
		id := fmt.Sprintf("race-%d", r)
		call := &callState{id: id, callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging, callType: CallTypeVoice}
		if !h.calls.reserve(call) {
			t.Fatalf("round %d: the previous call still holds a participant's slot", r)
		}
		a, b := sideClient(h, "uid_a", "app-a"), sideClient(h, "uid_b", "app-b")
		h.mu.Lock()
		h.clients["uid_a"], h.clients["uid_b"] = map[string]*Client{"": a}, map[string]*Client{"": b}
		h.mu.Unlock()

		start := make(chan struct{})
		var wg sync.WaitGroup
		run := func(f func()) {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				f()
			}()
		}
		msg := func(action, reqID string) *ClientMessage {
			return &ClientMessage{Id: reqID, Action: action, CallSessionId: id}
		}
		run(func() { h.handleCallAccept(b, msg(ActionCallAccept, "accept")) })
		run(func() { h.handleCallHangup(a, msg(ActionCallHangup, "hangup")) })
		run(func() { h.handleCallCancel(a, msg(ActionCallCancel, "cancel")) })
		run(func() { h.handleCallReject(b, msg(ActionCallReject, "reject")) })
		run(func() {
			call.startRingTimer(time.Duration(r%3)*time.Millisecond, func() { h.handleCallRingTimeout(call) })
		})
		if r%2 == 1 {
			run(func() { h.forceEndCallForDisconnect([]string{"uid_a", "uid_b"}[r/2%2]) })
		}
		close(start)
		wg.Wait()
		// Whatever is still open (an answered call nobody hung up) ends
		// through a disconnect.
		h.forceEndCallForDisconnect("uid_a")

		if _, status := call.currentTypeAndStatus(); status != callStateEnded {
			t.Fatalf("round %d: status %s", r, status)
		}
		if h.calls.get(id) != nil || h.calls.getByUID("uid_a") != nil || h.calls.getByUID("uid_b") != nil {
			t.Fatalf("round %d: the ended call still holds a slot", r)
		}
		for len(b.send) > 0 {
			if f := nextFrame(t, b); f.str("id") == "accept" && f.num("code") == CodeOK {
				accepted[id] = true
			}
		}
	}

	var settled map[string][]string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if settled = callHost.settlements(); len(settled) == rounds || time.Now().After(deadline) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond) // room for a second settlement to show up
	settled = callHost.settlements()
	outcomes := map[string]int{}
	for r := 0; r < rounds; r++ {
		id := fmt.Sprintf("race-%d", r)
		reasons := settled[id]
		if len(reasons) != 1 {
			t.Fatalf("%s settled %d times: %v", id, len(reasons), reasons)
		}
		reason := reasons[0]
		outcomes[reason]++
		answeredEnd := reason == "user_hangup" || reason == "disconnected"
		if accepted[id] && !answeredEnd {
			t.Fatalf("%s was accepted but settled as %s", id, reason)
		}
	}
	t.Logf("outcomes over %d rounds: %v", rounds, outcomes)
}

// R3, deterministically: a replaced connection's frame that was already read
// when it was replaced (a sub, a leave or a pub) must not take the topic slot
// from, or remove it under, the connection that replaced it.
func TestR3_LateFrameFromReplacedConnection(t *testing.T) {
	for _, action := range []string{ActionSub, ActionLeave, ActionPub} {
		t.Run(action, func(t *testing.T) {
			ls := newLiveServer(t, liveOptions{})
			o := ls.connect("r3late-o")
			uidX, err := ls.store.EnsureUser("r3late-x")
			if err != nil {
				t.Fatal(err)
			}
			group, err := ls.store.CreateGroup(o.uid, "late", []string{uidX})
			if err != nil {
				t.Fatal(err)
			}
			if reply := o.request(frame{"action": ActionSub, "topic": group}); reply.num("code") != CodeOK {
				t.Fatalf("sub: %v", reply)
			}

			old := ls.connect("r3late-x")
			var oldClient *Client
			for deadline := time.Now().Add(5 * time.Second); oldClient == nil; time.Sleep(time.Millisecond) {
				oldClient = ls.hub.clientOf(uidX, "")
				if time.Now().After(deadline) {
					t.Fatal("the first connection was never registered")
				}
			}
			if reply := old.request(frame{"action": ActionSub, "topic": group}); reply.num("code") != CodeOK {
				t.Fatalf("old sub: %v", reply)
			}

			replacement := ls.connect("r3late-x")
			if !old.waitClosed(5 * time.Second) {
				t.Fatal("the replaced connection was not closed")
			}
			for deadline := time.Now().Add(5 * time.Second); ls.hub.clientOf(uidX, "") == oldClient; time.Sleep(time.Millisecond) {
				if time.Now().After(deadline) {
					t.Fatal("the replacement was never registered")
				}
			}
			if reply := replacement.request(frame{"action": ActionSub, "topic": group}); reply.num("code") != CodeOK {
				t.Fatalf("replacement sub: %v", reply)
			}

			// The replaced connection's last frame is handled now.
			late := &ClientMessage{Id: "late", Action: action, Topic: group}
			if action == ActionPub {
				late.Content, late.ContentType = "late", "text/plain"
			}
			ls.hub.ProcessMessage(oldClient, late)

			if reply := o.request(frame{"action": ActionPub, "topic": group, "content": "after the late frame", "contentType": "text/plain"}); reply.num("code") != CodeOK {
				t.Fatalf("pub: %v", reply)
			}
			if _, ok := replacement.waitFor(dataWith("content", "after the late frame"), 3*time.Second); !ok {
				t.Fatalf("a late %s from the replaced connection cut the replacement off the group", action)
			}
		})
	}
}

// F60, the index rules on their own: a connection with an older login never
// takes or clears a newer one's slot, and a closed connection is not indexed.
func TestR3_TopicIndexRules(t *testing.T) {
	h := &Hub{topics: map[string]map[connKey]*Client{}}
	older, newer, closed := sideClient(h, "uid_x", "app-x"), sideClient(h, "uid_x", "app-x"), sideClient(h, "uid_x", "app-x")
	older.gen.Store(1)
	newer.gen.Store(2)
	closed.gen.Store(3)
	closed.closed = true
	slot := func(topic string) *Client { return h.topics[topic][connKey{"uid_x", ""}] }

	newer.Subscribe("grp_t")
	older.Subscribe("grp_t")
	if slot("grp_t") != newer {
		t.Fatal("an older login took a newer one's slot")
	}
	older.Unsubscribe("grp_t")
	if slot("grp_t") != newer {
		t.Fatal("an older login's leave cleared a newer one's slot")
	}
	older.Subscribe("grp_u")
	if slot("grp_u") != older {
		t.Fatal("a slot nobody newer holds was refused")
	}
	newer.Subscribe("grp_u")
	if slot("grp_u") != newer {
		t.Fatal("a newer login did not take the slot from an older one")
	}
	closed.Subscribe("grp_v")
	if _, ok := h.topics["grp_v"]; ok {
		t.Fatal("a closed connection was indexed")
	}
	newer.Unsubscribe("grp_t")
	if _, ok := h.topics["grp_t"]; ok {
		t.Fatal("the topic outlived its last subscriber")
	}
}

// F61: the first logins of one user arriving together all succeed and agree
// on the uid.
func TestEnsureUserConcurrentFirstLogin(t *testing.T) {
	s := testStore(t)
	for r := 0; r < 50; r++ {
		app := fmt.Sprintf("first-login-%d", r)
		uids := make([]string, 8)
		errs := make([]error, len(uids))
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range uids {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				uids[i], errs[i] = s.EnsureUser(app)
			}(i)
		}
		close(start)
		wg.Wait()
		for i := range uids {
			if errs[i] != nil {
				t.Fatalf("round %d: %v", r, errs[i])
			}
			if uids[i] != uids[0] {
				t.Fatalf("round %d: uids %s and %s for one user", r, uids[0], uids[i])
			}
		}
	}
}
