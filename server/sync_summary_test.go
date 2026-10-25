package main

import (
	"testing"
	"time"
)

// sync_summary (strict profile, protocol specification 8.1): every
// conversation of the caller with what a reconnecting client needs to decide
// whether to pull it, and a subscription to each, so that skipping the get of
// an unchanged conversation loses none of its live frames.

func summaryOf(t *testing.T, c *liveConn) map[string]frame {
	t.Helper()
	reply := c.request(frame{"action": ActionSyncSummary})
	if reply.num("code") != CodeOK {
		t.Fatalf("sync_summary: %v", reply)
	}
	out := map[string]frame{}
	list, _ := reply["topics"].([]any)
	for _, x := range list {
		f := frame(x.(map[string]any))
		out[f.str("topic")] = f
	}
	return out
}

func pubText(t *testing.T, c *liveConn, topic, text string) int64 {
	t.Helper()
	ack := c.request(frame{"action": ActionPub, "topic": topic, "content": text, "contentType": "text/plain"})
	if ack.num("code") != CodeOK {
		t.Fatalf("pub to %s: %v", topic, ack)
	}
	return ack.num("seq")
}

// legacy answers like the reference server, which has no such action; strict
// advertises it in the login reply.
func TestSyncSummary_OnlyUnderStrict(t *testing.T) {
	legacy := newProfileServer(t, false).connect("ss-legacy")
	reply := legacy.request(frame{"action": ActionSyncSummary})
	if reply.num("code") != CodeBadReq || reply.str("reason") != "unknown action: "+ActionSyncSummary {
		t.Fatalf("legacy: %v", reply)
	}
}

func TestSyncSummary_ListsEveryConversation(t *testing.T) {
	ls := newProfileServer(t, true)
	peer := ls.connect("ss-peer")
	phone := ls.connectDevice("ss-me", "phone")
	me := phone.uid
	stranger := ls.connect("ss-stranger")

	// A direct chat: two messages, the first read on the phone.
	subscribe(t, me, peer)
	first := pubText(t, peer, me, "d1")
	pubText(t, peer, me, "d2")
	readOn(t, phone, peer, peer.uid, first)
	p2p := ls.store.GetP2PTopic(me, peer.uid)

	// A group with one message, then cleared.
	group, err := ls.store.CreateGroup(peer.uid, "g", []string{me})
	if err != nil {
		t.Fatal(err)
	}
	pubText(t, peer, group, "g1")
	if r := peer.request(frame{"action": ActionDelTopic, "topic": group}); r.num("code") != CodeOK {
		t.Fatalf("del_topic: %v", r)
	}

	// A conversation I am not in.
	pubText(t, peer, stranger.uid, "not for me")

	tablet := ls.connectDevice("ss-me", "tablet")
	s := summaryOf(t, tablet)
	if len(s) != 2 {
		t.Fatalf("want the direct chat and the group, got %v", s)
	}
	d := s[p2p]
	if d == nil || d.num("lastSeq") != 2 || d.str("peer") != peer.uid || d.num("readSeq") != first || d["isGroup"] != false {
		t.Fatalf("direct chat %s: %v", p2p, d)
	}
	g := s[group]
	if g == nil || g.num("lastSeq") != 1 || g["isGroup"] != true || g.num("clearVersion") != 1 || g.num("clearBeforeSeq") != 1 {
		t.Fatalf("group %s: %v", group, g)
	}
	// Read receipts are off: a group keeps no read positions, as on a get.
	if g["readSeq"] != nil || g["readsAt"] != nil || g["peer"] != nil {
		t.Fatalf("group carries read or peer fields: %v", g)
	}

	// Nobody's conversations: topics is omitted.
	lonely := ls.connect("ss-lonely")
	if reply := lonely.request(frame{"action": ActionSyncSummary}); reply.num("code") != CodeOK || reply["topics"] != nil {
		t.Fatalf("no conversations: %v", reply)
	}
}

// A connection that asked for the summary gets the live frames of every
// conversation, without a sub or a get.
func TestSyncSummary_SubscribesEveryConversation(t *testing.T) {
	ls := newProfileServer(t, true)
	peer := ls.connect("sss-peer")
	phone := ls.connectDevice("sss-me", "phone")
	me := phone.uid
	group, err := ls.store.CreateGroup(peer.uid, "g", []string{me})
	if err != nil {
		t.Fatal(err)
	}
	subscribe(t, group, peer)
	direct := pubText(t, peer, me, "d1")

	tablet := ls.connectDevice("sss-me", "tablet")
	isData := func(topic string) func(frame) bool {
		return func(f frame) bool { return f.str("action") == ActionData && f.str("topic") == topic }
	}
	// Not subscribed yet: a group message does not reach the new connection.
	pubText(t, peer, group, "before")
	if f, ok := tablet.waitFor(isData(group), 300*time.Millisecond); ok {
		t.Fatalf("an unsubscribed connection got %v", f)
	}

	summaryOf(t, tablet)
	pubText(t, peer, group, "after")
	if _, ok := tablet.waitFor(func(f frame) bool { return isData(group)(f) && f.str("content") == "after" }, 5*time.Second); !ok {
		t.Fatal("the group message after the summary did not arrive")
	}
	// The direct chat too: a recall reaches only subscribed connections.
	if r := peer.request(frame{"action": ActionDel, "topic": me, "seq": direct}); r.num("code") != CodeOK {
		t.Fatalf("del: %v", r)
	}
	if _, ok := tablet.waitFor(func(f frame) bool { return f.str("action") == ActionRecalled && f.num("seq") == direct }, 5*time.Second); !ok {
		t.Fatal("the recall in the direct chat did not arrive")
	}
}

// With read receipts on, a group carries the caller's own read position and
// when another member last moved a read cursor; the caller's own reads change
// only the former.
func TestSyncSummary_ReadsWithReadReceipts(t *testing.T) {
	rc := defaultRuntimeConfig()
	rc.ReadReceiptEnabled = true
	setRuntimeConfig(rc)
	defer setRuntimeConfig(defaultRuntimeConfig())

	ls := newProfileServer(t, true)
	owner := ls.connect("ssr-owner")
	phone := ls.connectDevice("ssr-me", "phone")
	reader := ls.connect("ssr-reader")
	group, err := ls.store.CreateGroup(owner.uid, "g", []string{phone.uid, reader.uid})
	if err != nil {
		t.Fatal(err)
	}
	subscribe(t, group, owner, phone, reader)
	seq := pubText(t, owner, group, "g1")

	if g := summaryOf(t, phone)[group]; g["readsAt"] != nil || g["readSeq"] != nil {
		t.Fatalf("nobody has read yet: %v", g)
	}
	readOn(t, reader, owner, group, seq)
	at := summaryOf(t, phone)[group].num("readsAt")
	if at <= 0 {
		t.Fatalf("after another member's read: readsAt %d", at)
	}

	readOn(t, phone, owner, group, seq)
	g := summaryOf(t, phone)[group]
	if g.num("readSeq") != seq || g.num("readsAt") != at {
		t.Fatalf("after my own read: want readSeq %d and readsAt %d unchanged, got %v", seq, at, g)
	}
}
