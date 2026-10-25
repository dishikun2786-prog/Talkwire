package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"example.com/imkit/server/host"
	"example.com/imkit/server/migrate"
	"example.com/imkit/server/migrations"
	"example.com/imkit/server/push"
)

func mustE2eeMigrations(t *testing.T) migrate.Set {
	t.Helper()
	set, err := migrations.E2ee()
	if err != nil {
		t.Fatal(err)
	}
	return set
}

// Several devices per user (task 1.12).

func isData(content string) func(frame) bool {
	return func(f frame) bool { return f.str("action") == ActionData && f.str("content") == content }
}

// A login names its device; only a new login of the same device replaces a
// connection, and logins that name none replace each other as before.
func TestMultiDevice_OnlyTheSameDeviceIsReplaced(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	o := ls.connect("mdr-o")
	phone := ls.connectDevice("mdr-u", "phone")
	web := ls.connectDevice("mdr-u", "web")
	uidU := phone.uid
	legacy := ls.connect("mdr-u")
	ls.waitRegistered(uidU, "")

	phone2 := ls.connectDevice("mdr-u", "phone")
	if !phone.waitClosed(5 * time.Second) {
		t.Fatal("a new login of the same device did not replace the old connection")
	}
	legacy2 := ls.connect("mdr-u")
	if !legacy.waitClosed(5 * time.Second) {
		t.Fatal("a login without a device did not replace the previous one")
	}
	ls.waitRegistered(uidU, "")

	// Every remaining device is connected and receives the user's messages.
	if reply := o.request(frame{"action": ActionSub, "topic": uidU}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	if reply := o.request(frame{"action": ActionPub, "topic": uidU, "content": "to every device", "contentType": "text/plain"}); reply.num("code") != CodeOK {
		t.Fatalf("pub: %v", reply)
	}
	for name, c := range map[string]*liveConn{"phone": phone2, "web": web, "legacy": legacy2} {
		if f, ok := c.waitFor(isData("to every device"), 5*time.Second); !ok || f.str("topic") != o.uid {
			t.Fatalf("%s: %v, %v", name, f, ok)
		}
	}
}

func TestMultiDevice_InvalidDeviceIsRefused(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	for _, device := range []string{"has space", "emoji-☃", strings.Repeat("d", maxDeviceLen+1)} {
		c, _, err := ls.dial("/ws")
		if err != nil {
			t.Fatal(err)
		}
		if reply := c.loginFrom("mdi-u", device); reply.num("code") != CodeBadReq || reply.str("reason") != "invalid device" {
			t.Errorf("device %q: %v", device, reply)
		}
	}
	c, _, err := ls.dial("/ws")
	if err != nil {
		t.Fatal(err)
	}
	if reply := c.loginFrom("mdi-u", strings.Repeat("d", maxDeviceLen)); reply.num("code") != CodeOK {
		t.Errorf("a %d-character device was refused: %v", maxDeviceLen, reply)
	}
}

// The user is online while any device is connected: friends see one "off",
// when the last device leaves.
func TestMultiDevice_PresenceAcrossDevices(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	o := ls.connect("mdp-o")
	uidU, err := ls.store.EnsureUser("mdp-u")
	if err != nil {
		t.Fatal(err)
	}
	if reply := o.request(frame{"action": ActionSub, "topic": uidU}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	// The sub is followed by the user's current state ("off").
	if _, ok := o.waitFor(presOf(uidU), 5*time.Second); !ok {
		t.Fatal("no presence after sub")
	}
	before := len(o.frames(presOf(uidU)))
	// A friend on two devices hears it on both.
	tablet := ls.connectDevice("mdp-o", "tablet")
	if reply := tablet.request(frame{"action": ActionSub, "topic": uidU}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	if _, ok := tablet.waitFor(presOf(uidU), 5*time.Second); !ok {
		t.Fatal("no presence after sub")
	}
	tabletBefore := len(tablet.frames(presOf(uidU)))

	phone := ls.connectDevice("mdp-u", "phone")
	web := ls.connectDevice("mdp-u", "web")
	web.close()
	for deadline := time.Now().Add(5 * time.Second); ls.hub.clientOf(uidU, "web") != nil; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("web never unregistered")
		}
	}
	o.send(frame{"action": ActionPres, "topic": uidU})
	snap, ok := o.waitFor(func(f frame) bool { return f.str("what") == PresSnapshot }, 5*time.Second)
	if !ok {
		t.Fatal("no snapshot")
	}
	if st := frame(snap["states"].([]any)[0].(map[string]any)); st["online"] != true {
		t.Fatalf("the user is reported offline with a device connected: %v", st)
	}

	phone.close()
	for deadline := time.Now().Add(5 * time.Second); ls.hub.isOnline(uidU); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the phone never unregistered")
		}
	}
	// Presence pushes go out in order: once the next login's "on" arrives,
	// every earlier push has.
	ls.connectDevice("mdp-u", "phone")
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(time.Millisecond) {
		pushes := o.frames(presOf(uidU))[before:]
		if len(pushes) >= 4 && pushes[len(pushes)-1].str("what") == PresOnline {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pushes so far: %d", len(pushes))
		}
	}
	var whats []string
	for _, f := range o.frames(presOf(uidU))[before:] {
		whats = append(whats, f.str("what"))
	}
	if strings.Join(whats, ",") != "on,on,off,on" {
		t.Fatalf("presence pushes %v, want on,on,off,on (one off, when the last device left)", whats)
	}
	if _, ok := tablet.waitFor(func(f frame) bool { return presOf(uidU)(f) && f.str("what") == PresOffline }, 5*time.Second); !ok {
		t.Fatal("the friend's other device missed the offline")
	}
	if n := len(tablet.frames(presOf(uidU))[tabletBefore:]); n < 3 {
		t.Fatalf("the friend's other device got %d presence pushes, want every one", n)
	}
	assertRevsIncrease(t, uidU, o.frames(presOf(uidU))[before:])
}

// A user's other devices see what they send, read and recall; typing stays
// with the peers. P2P frames name the conversation the way each device keys
// it.
func TestMultiDevice_OwnActivityReachesOtherDevices(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	o := ls.connect("mdm-o")
	phone := ls.connectDevice("mdm-u", "phone")
	web := ls.connectDevice("mdm-u", "web")
	uidU := phone.uid
	// Subscribing on one device follows the conversation on the others.
	if reply := phone.request(frame{"action": ActionSub, "topic": o.uid}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	if reply := o.request(frame{"action": ActionSub, "topic": uidU}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}

	ack := phone.request(frame{"action": ActionPub, "topic": o.uid, "content": "from phone", "contentType": "text/plain"})
	if ack.num("code") != CodeOK {
		t.Fatalf("pub: %v", ack)
	}
	if f, ok := o.waitFor(isData("from phone"), 5*time.Second); !ok || f.str("topic") != uidU {
		t.Fatalf("the peer got %v, %v", f, ok)
	}
	echo, ok := web.waitFor(isData("from phone"), 5*time.Second)
	if !ok || echo.str("topic") != o.uid || echo.str("from") != uidU || echo.num("seq") != ack.num("seq") {
		t.Fatalf("the sender's other device got %v, %v", echo, ok)
	}
	if n := len(phone.frames(isData("from phone"))); n != 0 {
		t.Fatalf("the sending device got its own message %d times", n)
	}

	group, err := ls.store.CreateGroup(o.uid, "md", []string{uidU})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []*liveConn{o, phone, web} {
		if reply := c.request(frame{"action": ActionSub, "topic": group}); reply.num("code") != CodeOK {
			t.Fatalf("sub: %v", reply)
		}
	}
	gack := phone.request(frame{"action": ActionPub, "topic": group, "content": "group from phone", "contentType": "text/plain"})
	if f, ok := web.waitFor(isData("group from phone"), 5*time.Second); !ok || f.str("topic") != group {
		t.Fatalf("group echo: %v, %v", f, ok)
	}

	isNote := func(what string) func(frame) bool {
		return func(f frame) bool { return f.str("action") == ActionInfo && f.str("what") == what }
	}
	phone.send(frame{"action": ActionNote, "topic": group, "what": NoteRead, "seq": gack.num("seq")})
	if _, ok := web.waitFor(isNote(NoteRead), 5*time.Second); !ok {
		t.Fatal("a read on one device did not reach the other")
	}
	if _, ok := o.waitFor(isNote(NoteRead), 5*time.Second); !ok {
		t.Fatal("the read did not reach the peer")
	}
	phone.send(frame{"action": ActionNote, "topic": group, "what": "kp"})
	if _, ok := o.waitFor(isNote("kp"), 5*time.Second); !ok {
		t.Fatal("typing did not reach the peer")
	}
	if n := len(web.frames(isNote("kp"))); n != 0 {
		t.Fatal("the user's own typing reached their other device")
	}

	if reply := phone.request(frame{"action": ActionDel, "topic": group, "seq": gack.num("seq")}); reply.num("code") != CodeOK {
		t.Fatalf("recall: %v", reply)
	}
	if _, ok := web.waitFor(func(f frame) bool { return f.str("action") == ActionRecalled && f.num("seq") == gack.num("seq") }, 5*time.Second); !ok {
		t.Fatal("a recall on one device did not reach the other")
	}
}

// A clear made while one of the user's devices is away reaches that device
// at its next login, and the queue row goes once every device has it.
func TestMultiDevice_ClearQueuedForTheDeviceAway(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	o := ls.connect("mdc-o")
	web := ls.connectDevice("mdc-u", "web")
	uidU := web.uid
	web.close() // web is now a known device that is away
	for deadline := time.Now().Add(5 * time.Second); ls.hub.clientOf(uidU, "web") != nil; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("web never unregistered")
		}
	}
	phone := ls.connectDevice("mdc-u", "phone")
	if reply := phone.request(frame{"action": ActionSub, "topic": o.uid}); reply.num("code") != CodeOK {
		t.Fatalf("sub: %v", reply)
	}
	if reply := phone.request(frame{"action": ActionPub, "topic": o.uid, "content": "soon gone", "contentType": "text/plain"}); reply.num("code") != CodeOK {
		t.Fatalf("pub: %v", reply)
	}
	if reply := phone.request(frame{"action": ActionDelTopic, "topic": o.uid}); reply.num("code") != CodeOK {
		t.Fatalf("clear: %v", reply)
	}
	if f, ok := o.waitFor(func(f frame) bool { return f.str("action") == ActionTopicCleared }, 5*time.Second); !ok || f.str("topic") != uidU {
		t.Fatalf("the peer: %v, %v", f, ok)
	}
	rows := func() int {
		var n int
		if err := ls.store.db.QueryRow(`SELECT count(*) FROM im_pending_clears WHERE uid = $1`, uidU).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Queued right after the live sends.
	for deadline := time.Now().Add(5 * time.Second); rows() != 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d clears queued for the user, want 1 (for web)", rows())
		}
	}
	if n := len(phone.frames(func(f frame) bool { return f.str("action") == ActionTopicCleared })); n != 0 {
		t.Fatal("the device that cleared got the clear back")
	}

	// The phone logs in again: it already has the clear.
	phone.close()
	phone = ls.connectDevice("mdc-u", "phone")
	web = ls.connectDevice("mdc-u", "web")
	f, ok := web.waitFor(func(f frame) bool { return f.str("action") == ActionTopicCleared }, 5*time.Second)
	if !ok || f.str("topic") != o.uid {
		t.Fatalf("web at its next login: %v, %v", f, ok)
	}
	time.Sleep(50 * time.Millisecond) // the replay runs right after login
	if n := len(phone.frames(func(f frame) bool { return f.str("action") == ActionTopicCleared })); n != 0 {
		t.Fatal("the clear was replayed to the device that made it")
	}
	web.send(frame{"action": ActionAckTopicCleared, "id": f.str("id")})
	for deadline := time.Now().Add(5 * time.Second); rows() != 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the queued clear outlived its last device's acknowledgement")
		}
	}
}

// A group joined while one of the user's devices is away reaches that device
// at its next login.
func TestMultiDevice_GroupEventQueuedForTheDeviceAway(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	owner := ls.connect("mdg-owner")
	web := ls.connectDevice("mdg-u", "web")
	uidU := web.uid
	web.close()
	for deadline := time.Now().Add(5 * time.Second); ls.hub.clientOf(uidU, "web") != nil; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("web never unregistered")
		}
	}
	phone := ls.connectDevice("mdg-u", "phone")
	group, err := ls.store.CreateGroup(owner.uid, "md", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reply := owner.request(frame{"action": ActionInvite, "topic": group, "user": uidU}); reply.num("code") != CodeOK {
		t.Fatalf("invite: %v", reply)
	}
	if _, ok := phone.waitFor(func(f frame) bool { return f.str("action") == ActionGrpSync && f.str("topic") == group }, 5*time.Second); !ok {
		t.Fatal("the connected device did not hear of the group")
	}
	queued := func() int {
		var n int
		if err := ls.store.db.QueryRow(`SELECT count(*) FROM im_pending_group_events WHERE uid = $1`, uidU).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Queued right after the live sends.
	for deadline := time.Now().Add(5 * time.Second); queued() != 1; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d events queued for the user, want 1 (for web)", queued())
		}
	}
	web = ls.connectDevice("mdg-u", "web")
	ev, ok := web.waitFor(func(f frame) bool { return f.str("action") == ActionGrpSync && f.str("topic") == group }, 5*time.Second)
	if !ok || ev.num("eventId") == 0 {
		t.Fatalf("web at its next login: %v, %v", ev, ok)
	}
	// Replaying it subscribed web to the group.
	if reply := owner.request(frame{"action": ActionPub, "topic": group, "content": "welcome", "contentType": "text/plain"}); reply.num("code") != CodeOK {
		t.Fatalf("pub: %v", reply)
	}
	if _, ok := web.waitFor(isData("welcome"), 5*time.Second); !ok {
		t.Fatal("the replayed event did not subscribe the device")
	}
	web.send(frame{"action": actionAckGroupEvent, "eventId": ev.num("eventId")})
	for deadline := time.Now().Add(5 * time.Second); queued() != 0; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the queued event outlived its last device's acknowledgement")
		}
	}
}

// Push goes to the devices that are away; a device keeps one token per
// provider.
func TestMultiDevice_PushTargetsAndTokens(t *testing.T) {
	s := testStore(t)
	h := &Hub{store: s, clients: map[string]map[string]*Client{}}
	uid := "uid_push"
	for _, r := range []struct{ token, device string }{{"t-phone-old", "phone"}, {"t-phone", "phone"}, {"t-web", "web"}, {"t-legacy-1", ""}, {"t-legacy-2", ""}} {
		if err := s.SavePushToken(uid, push.FCM, r.token, r.device); err != nil {
			t.Fatal(err)
		}
	}
	values := func(ts []push.Token) string {
		var out []string
		for _, t := range ts {
			out = append(out, t.Value)
		}
		return strings.Join(sortedStrings(out), ",")
	}
	if got := values(h.pushTargets([]string{uid}, "test")[uid]); got != "t-legacy-1,t-legacy-2,t-phone,t-web" {
		t.Fatalf("offline user: %s (the phone keeps one token)", got)
	}
	h.clients[uid] = map[string]*Client{"phone": {}}
	if got := values(h.pushTargets([]string{uid}, "test")[uid]); got != "t-legacy-1,t-legacy-2,t-web" {
		t.Fatalf("phone connected: %s, want the web and device-less tokens", got)
	}
	h.clients[uid] = map[string]*Client{"phone": {}, "web": {}}
	if got := values(h.pushTargets([]string{uid}, "test")[uid]); got != "t-legacy-1,t-legacy-2" {
		t.Fatalf("every named device connected: %s, want the device-less tokens", got)
	}
	h.clients[uid] = map[string]*Client{"phone": {}, "web": {}, "": {}}
	if got, ok := h.pushTargets([]string{uid}, "test")[uid]; ok {
		t.Fatalf("every device connected: %v", got)
	}
	h.clients[uid] = map[string]*Client{"": {}}
	if got := values(h.pushTargets([]string{uid}, "test")[uid]); got != "t-phone,t-web" {
		t.Fatalf("a login without a device connected: %s", got)
	}
	// An E2EE notice goes to the device holding the keys only.
	h.clients[uid] = map[string]*Client{"web": {}}
	if got := values(h.pushTargetsOn([]string{uid}, "test", map[string]string{uid: "phone"})[uid]); got != "t-phone" {
		t.Fatalf("owner phone away: %s, want only the phone token", got)
	}
	if got, ok := h.pushTargetsOn([]string{uid}, "test", map[string]string{uid: "web"})[uid]; ok {
		t.Fatalf("owner web connected: %v", got)
	}
}

func sortedStrings(s []string) []string {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
	return s
}

// attach puts a socketless connection of (uid, device) in the hub's
// connection table.
func attach(h *Hub, uid, device string) *Client {
	c := sideClient(h, uid, "app-"+uid)
	c.setDevice(device)
	h.mu.Lock()
	if h.clients[uid] == nil {
		h.clients[uid] = map[string]*Client{}
	}
	h.clients[uid][device] = c
	h.mu.Unlock()
	return c
}

func detach(h *Hub, c *Client) {
	h.mu.Lock()
	delete(h.clients[c.UID()], c.Device())
	if len(h.clients[c.UID()]) == 0 {
		delete(h.clients, c.UID())
	}
	h.mu.Unlock()
}

// drain returns the frames a socketless connection has been sent.
func drain(c *Client) []frame {
	var out []frame
	for len(c.send) > 0 {
		var f frame
		if err := json.Unmarshal(<-c.send, &f); err == nil {
			out = append(out, f)
		}
	}
	return out
}

// E2EE stays on the device that published the identity: envelopes go to it
// only, wait for it while it is away, and are replayed to it only.
func TestMultiDevice_E2EEStaysOnItsDevice(t *testing.T) {
	s := testStore(t)
	if err := s.InitTables(mustE2eeMigrations(t)); err != nil {
		t.Fatal(err)
	}
	h := &Hub{store: s, clients: map[string]map[string]*Client{}, topics: map[string]map[connKey]*Client{}}
	const uid = "uid_e2e"
	phone := attach(h, uid, "phone")
	web := attach(h, uid, "web")
	if err := s.UpsertE2eIdentity(uid, 1, &E2ePublishBundlePayload{IdentityKey: "ik", SignedPreKey: "spk", SignedPreKeySignature: "sig", KyberPreKey: "kpk", KyberPreKeySignature: "ksig"}, "phone"); err != nil {
		t.Fatal(err)
	}
	isDeliver := func(f frame) bool { return f.str("action") == ActionE2eDeliver }
	count := func(fs []frame) int {
		n := 0
		for _, f := range fs {
			if isDeliver(f) {
				n++
			}
		}
		return n
	}

	h.relayE2eEnvelope("p2p_x", "uid_peer", uid, "msg", "ciphertext-1", 1)
	if got := count(drain(phone)); got != 1 {
		t.Fatalf("the identity's device got %d envelopes", got)
	}
	if got := count(drain(web)); got != 0 {
		t.Fatal("a device without the keys got an envelope")
	}

	detach(h, phone)
	h.relayE2eEnvelope("p2p_x", "uid_peer", uid, "msg", "ciphertext-2", 1)
	if got := count(drain(web)); got != 0 {
		t.Fatal("an envelope went to another device while the identity's device was away")
	}
	h.deliverPendingE2eEnvelopes(web)
	if got := count(drain(web)); got != 0 {
		t.Fatal("the queue was replayed to a device without the keys")
	}
	phone = attach(h, uid, "phone")
	h.deliverPendingE2eEnvelopes(phone)
	if got := count(drain(phone)); got != 1 {
		t.Fatalf("the identity's device got %d queued envelopes at its next login", got)
	}
}

// answeringCallHost answers every call.
type answeringCallHost struct{ nopHost }

func (answeringCallHost) AnswerCallSession(string, string) (host.AnswerCallSessionResult, error) {
	return host.AnswerCallSessionResult{Ok: true}, nil
}

// A call rings every device of the callee. The device that answers takes it:
// the others stop ringing, upgrades go between the two devices in the call,
// and only those devices leaving ends it.
func TestMultiDevice_CallRingsEveryDeviceAndStaysOnTheOneThatAnswered(t *testing.T) {
	withShortCallDisconnectGrace(30*time.Millisecond, func() {
		h := newTestHubForCallDisconnect()
		if _, err := installModules(h, answeringCallHost{}, []string{"call"}, true); err != nil {
			t.Fatal(err)
		}
		callerPhone := attach(h, "uid_a", "phone")
		callerWeb := attach(h, "uid_a", "web")
		calleePhone := attach(h, "uid_b", "phone")
		calleeWeb := attach(h, "uid_b", "web")
		call := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging, callType: CallTypeVoice, callerDevice: "phone"}
		h.calls.reserve(call)
		ring := &CallSignalMessage{Type: ActionCallRing, CallSessionId: "s1"}
		h.sendToUser("uid_b", ring, nil)
		for _, c := range []*Client{calleePhone, calleeWeb} {
			if fs := drain(c); len(fs) != 1 || fs[0].str("type") != ActionCallRing {
				t.Fatalf("a callee device did not ring: %v", fs)
			}
		}

		h.handleCallAccept(calleeWeb, &ClientMessage{Id: "acc", Action: ActionCallAccept, CallSessionId: "s1"})
		if fs := drain(calleeWeb); len(fs) != 1 || fs[0].num("code") != CodeOK {
			t.Fatalf("accept: %v", fs)
		}
		if fs := drain(calleePhone); len(fs) != 1 || fs[0].str("type") != ActionCallEnded || fs[0].str("reason") != "answered_elsewhere" {
			t.Fatalf("the device that did not answer: %v", fs)
		}
		if fs := drain(callerPhone); len(fs) != 1 || fs[0].str("type") != ActionCallAnswered {
			t.Fatalf("the inviting device: %v", fs)
		}
		if fs := drain(callerWeb); len(fs) != 0 {
			t.Fatalf("the caller's other device: %v", fs)
		}

		h.handleCallUpgrade(callerPhone, &ClientMessage{Id: "up", Action: ActionCallUpgrade, CallSessionId: "s1", ToType: CallTypeVideo})
		if fs := drain(calleeWeb); len(fs) != 1 || fs[0].str("type") != ActionCallUpgradeRequest {
			t.Fatalf("the upgrade request went to %v", fs)
		}
		if fs := drain(calleePhone); len(fs) != 0 {
			t.Fatalf("the upgrade request reached a device not in the call: %v", fs)
		}

		// The callee's other device leaving changes nothing.
		detach(h, calleePhone)
		h.mods.disconnected(h, "uid_b", "phone")
		if _, scheduled := h.pendingCallDisconnects.Load("uid_b"); scheduled {
			t.Fatal("a device not in the call started the disconnect grace period")
		}
		time.Sleep(100 * time.Millisecond)
		if _, status := call.currentTypeAndStatus(); status != callStateActive {
			t.Fatalf("a device not in the call ended it: %s", status)
		}
		// The device in the call leaving ends it after the grace period.
		detach(h, calleeWeb)
		h.mods.disconnected(h, "uid_b", "web")
		time.Sleep(150 * time.Millisecond)
		if _, status := call.currentTypeAndStatus(); status != callStateEnded {
			t.Fatalf("the call outlived the device it was on: %s", status)
		}
	})
}

// Under concurrency several devices of one user publishing the same
// clientMsgId still store and broadcast one message (R1 with real devices).
func TestMultiDevice_DuplicatePublishFromTwoDevices(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	b := ls.connect("mdd-b")
	for r := 0; r < 20; r++ {
		app := fmt.Sprintf("mdd-a-%d", r)
		phone := ls.connectDevice(app, "phone")
		web := ls.connectDevice(app, "web")
		if reply := b.request(frame{"action": ActionSub, "topic": phone.uid}); reply.num("code") != CodeOK {
			t.Fatalf("sub: %v", reply)
		}
		cmid := fmt.Sprintf("two-devices-%d", r)
		var wg sync.WaitGroup
		for _, c := range []*liveConn{phone, web} {
			wg.Add(1)
			go func(c *liveConn) {
				defer wg.Done()
				c.send(frame{"action": ActionPub, "id": "p", "topic": b.uid, "content": "once", "contentType": "text/plain", "clientMsgId": cmid})
			}(c)
		}
		wg.Wait()
		seqs := map[int64]bool{}
		for _, c := range []*liveConn{phone, web} {
			ack, ok := c.waitFor(func(f frame) bool { return f.str("id") == "p" }, 5*time.Second)
			if !ok || ack.num("code") != CodeOK {
				t.Fatalf("round %d: %v", r, ack)
			}
			seqs[ack.num("seq")] = true
		}
		if len(seqs) != 1 {
			t.Fatalf("round %d: two devices got seqs %v for one message", r, seqs)
		}
		if reply := phone.request(frame{"action": ActionPub, "topic": b.uid, "content": "after", "contentType": "text/plain", "clientMsgId": cmid + "-end"}); reply.num("code") != CodeOK {
			t.Fatalf("pub: %v", reply)
		}
		if _, ok := b.waitFor(dataWith("clientMsgId", cmid+"-end"), 5*time.Second); !ok {
			t.Fatal("no closing message")
		}
		if n := len(b.frames(dataWith("clientMsgId", cmid))); n != 1 {
			t.Fatalf("round %d: the peer received the message %d times", r, n)
		}
	}
}

// A queued row is pending per device: each device gets it once, and the row
// goes when every device seen in the last 30 days has it. A newer change to
// the same row is new to every device again.
func TestMultiDevice_QueueAcksPerDevice(t *testing.T) {
	s := testStore(t)
	const uid = "uid_q"
	for _, d := range []string{"a", "b", "stale"} {
		if err := s.EnsureDevice(uid, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE im_devices SET last_seen_at = NOW() - INTERVAL '31 days' WHERE device_id = 'stale'`); err != nil {
		t.Fatal(err)
	}
	pending := func(device string) []PendingClear {
		l, err := s.GetPendingClears(uid, device)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	if err := s.EnqueuePendingClear("p2p_q", uid, "uid_peer", 1, 5, "uid_peer", []string{"a"}); err != nil {
		t.Fatal(err)
	}
	if len(pending("a")) != 0 || len(pending("b")) != 1 {
		t.Fatal("the device that got the clear live has it queued, or the other one does not")
	}
	// A newer clear of the same conversation is new to "a" again.
	if err := s.EnqueuePendingClear("p2p_q", uid, "uid_peer", 2, 9, "uid_peer", nil); err != nil {
		t.Fatal(err)
	}
	if l := pending("a"); len(l) != 1 || l[0].ClearVersion != 2 {
		t.Fatalf("the newer clear for a: %v", l)
	}
	// An older one changes nothing.
	if err := s.EnqueuePendingClear("p2p_q", uid, "uid_peer", 1, 5, "uid_peer", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if len(pending("b")) != 1 {
		t.Fatal("an older clear marked the newer one delivered")
	}
	id := pending("a")[0].ID
	if err := s.AckPendingClear(id, uid, "a"); err != nil {
		t.Fatal(err)
	}
	if len(pending("a")) != 0 || len(pending("b")) != 1 {
		t.Fatal("one device's acknowledgement removed the row for the other")
	}
	if err := s.AckPendingClear(id, "uid_someone_else", "b"); err != nil {
		t.Fatal(err)
	}
	if len(pending("b")) != 1 {
		t.Fatal("another user acknowledged the row")
	}
	if err := s.AckPendingClear(id, uid, "b"); err != nil {
		t.Fatal(err)
	}
	var rows, acks int
	s.db.QueryRow(`SELECT count(*) FROM im_pending_clears WHERE uid = $1`, uid).Scan(&rows)
	s.db.QueryRow(`SELECT count(*) FROM im_pending_acks WHERE item_id = $1`, id).Scan(&acks)
	if rows != 0 || acks != 0 {
		t.Fatalf("after every recent device acknowledged: %d rows, %d acks (a device unseen for 31 days must not hold the row)", rows, acks)
	}
}

// The acting device is skipped by device, not by connection: a late frame
// from a connection its device has since replaced must not echo the action
// back to that device's new connection, as the reference server skipped
// every connection of the acting user.
func TestMultiDevice_ReplacedConnectionDoesNotEchoToItsDevice(t *testing.T) {
	h := &Hub{clients: map[string]map[string]*Client{}, topics: map[string]map[connKey]*Client{}}
	replaced := sideClient(h, "uid_a", "app-uid_a")
	replaced.setDevice("phone")
	phone := attach(h, "uid_a", "phone")
	web := attach(h, "uid_a", "web")
	msg := &ServerMessage{Action: ActionInfo, Topic: "uid_b", From: "uid_a", What: NoteRead}

	if reached := h.sendToUser("uid_a", msg, replaced); len(reached) != 1 || reached[0] != "web" {
		t.Fatalf("sendToUser reached %v, want only the web", reached)
	}
	if fs := drain(phone); len(fs) != 0 {
		t.Fatalf("the device that acted got its own action back: %v", fs)
	}
	if fs := drain(web); len(fs) != 1 {
		t.Fatalf("the other device: %v", fs)
	}

	phone.Subscribe("grp_x")
	web.Subscribe("grp_x")
	h.BroadcastToTopicExcept("grp_x", msg, replaced)
	if fs := drain(phone); len(fs) != 0 {
		t.Fatalf("BroadcastToTopicExcept echoed to the acting device: %v", fs)
	}
	if fs := drain(web); len(fs) != 1 {
		t.Fatalf("BroadcastToTopicExcept missed the other device: %v", fs)
	}
}

// Only the devices in a call act on it and hear it end: another device of a
// participant is told the call does not exist, and call_ended reaches the
// device in the call (every device of a callee while it rings).
func TestMultiDevice_CallActionsStayOnTheDevicesInTheCall(t *testing.T) {
	h := newTestHubForCallDisconnect()
	if _, err := installModules(h, answeringCallHost{}, []string{"call"}, true); err != nil {
		t.Fatal(err)
	}
	callerPhone := attach(h, "uid_a", "phone")
	callerWeb := attach(h, "uid_a", "web")
	calleePhone := attach(h, "uid_b", "phone")
	calleeWeb := attach(h, "uid_b", "web")
	all := []*Client{callerPhone, callerWeb, calleePhone, calleeWeb}
	reply := func(c *Client) frame {
		t.Helper()
		fs := drain(c)
		if len(fs) != 1 {
			t.Fatalf("want one reply, got %v", fs)
		}
		return fs[0]
	}
	ended := func(c *Client, reason string) {
		t.Helper()
		if fs := drain(c); len(fs) != 1 || fs[0].str("type") != ActionCallEnded || fs[0].str("reason") != reason {
			t.Fatalf("want call_ended %q, got %v", reason, fs)
		}
	}
	quiet := func(cs ...*Client) {
		t.Helper()
		for _, c := range cs {
			if fs := drain(c); len(fs) != 0 {
				t.Fatalf("a device not in the call heard of it: %v", fs)
			}
		}
	}

	// Ringing: the caller's other device cannot cancel; a ring timeout ends
	// it on the inviting device and on every callee device.
	ringing := &callState{id: "s1", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging, callType: CallTypeVoice, callerDevice: "phone"}
	h.calls.reserve(ringing)
	h.handleCallCancel(callerWeb, &ClientMessage{Id: "c", Action: ActionCallCancel, CallSessionId: "s1"})
	if f := reply(callerWeb); f.num("code") != CodeNotFound {
		t.Fatalf("cancel from the caller's other device: %v", f)
	}
	h.handleCallRingTimeout(ringing)
	ended(callerPhone, "no_answer")
	ended(calleePhone, "no_answer")
	ended(calleeWeb, "no_answer")
	quiet(callerWeb)

	// Answered on the callee's web: only the inviting phone and the
	// answering web act on it.
	call := &callState{id: "s2", callerUID: "uid_a", calleeUID: "uid_b", status: callStateRinging, callType: CallTypeVoice, callerDevice: "phone"}
	h.calls.reserve(call)
	h.handleCallAccept(calleeWeb, &ClientMessage{Id: "acc", Action: ActionCallAccept, CallSessionId: "s2"})
	for _, c := range all {
		drain(c)
	}
	for _, c := range []*Client{callerWeb, calleePhone} {
		h.handleCallHangup(c, &ClientMessage{Id: "h", Action: ActionCallHangup, CallSessionId: "s2"})
		if f := reply(c); f.num("code") != CodeNotFound {
			t.Fatalf("hangup from a device not in the call: %v", f)
		}
		h.handleCallUpgrade(c, &ClientMessage{Id: "u", Action: ActionCallUpgrade, CallSessionId: "s2", ToType: CallTypeVideo})
		if f := reply(c); f.num("code") != CodeNotFound {
			t.Fatalf("upgrade from a device not in the call: %v", f)
		}
	}
	if _, status := call.currentTypeAndStatus(); status == callStateEnded {
		t.Fatal("a device not in the call ended it")
	}
	quiet(callerPhone, calleeWeb)
	h.handleCallHangup(callerPhone, &ClientMessage{Id: "h", Action: ActionCallHangup, CallSessionId: "s2"})
	if f := reply(callerPhone); f.num("code") != CodeOK {
		t.Fatalf("hangup from the inviting device: %v", f)
	}
	ended(calleeWeb, "user_hangup")
	quiet(callerWeb, calleePhone)
}

// A user is connected from at most maxDevicesPerUser devices: a login from a
// new device beyond it is refused, a login from a connected device still
// replaces it.
func TestMultiDevice_DeviceCap(t *testing.T) {
	prev := maxDevicesPerUser
	maxDevicesPerUser = 2 // before the server starts: its handlers read it
	t.Cleanup(func() { maxDevicesPerUser = prev })
	ls := newLiveServer(t, liveOptions{})
	phone := ls.connectDevice("mdcap-a", "phone")
	ls.connectDevice("mdcap-a", "web")

	c, _, err := ls.dial("/ws")
	if err != nil {
		t.Fatal(err)
	}
	if r := c.loginFrom("mdcap-a", "tablet"); r.num("code") != CodeConflict || r.str("reason") != "too many devices" {
		t.Fatalf("a third device: %v", r)
	}
	if ls.hub.clientOf(phone.uid, "tablet") != nil {
		t.Fatal("the refused device was registered")
	}
	// The connection can still log in from a device it may use.
	if r := c.loginFrom("mdcap-a", "phone"); r.num("code") != CodeOK {
		t.Fatalf("replacing a connected device: %v", r)
	}
	if !phone.waitClosed(5 * time.Second) {
		t.Fatal("the replaced phone connection stayed open")
	}
	ls.connectDevice("mdcap-b", "tablet") // the cap is per user
}

// Devices not seen for 30 days are forgotten.
func TestMultiDevice_StaleDevicesAreForgotten(t *testing.T) {
	s := testStore(t)
	uid := "uid_reap_devices"
	for _, d := range []string{"old", "new"} {
		if err := s.EnsureDevice(uid, d); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`UPDATE im_devices SET last_seen_at = NOW() - INTERVAL '31 days' WHERE uid = $1 AND device_id = 'old'`, uid); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ReapStaleDevices(time.Now()); err != nil || n != 1 {
		t.Fatalf("reaped %d, %v", n, err)
	}
	var left []string
	if err := func() error {
		rows, err := s.db.Query(`SELECT device_id FROM im_devices WHERE uid = $1`, uid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var d string
			if err := rows.Scan(&d); err != nil {
				return err
			}
			left = append(left, d)
		}
		return rows.Err()
	}(); err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0] != "new" {
		t.Fatalf("devices left: %v", left)
	}
}
