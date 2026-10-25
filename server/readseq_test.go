package main

import (
	"fmt"
	"testing"
	"time"
)

// The caller's own read position on a get reply (readSeq, strict profile):
// a direct chat's "read" note is stored as the sender's own cursor, so that a
// new device of the same user learns how far the user has read instead of
// counting the whole pulled history as unread. Legacy stores nothing and
// sends no readSeq, like the reference server.

func readNoteAt(seq int64) func(frame) bool {
	return func(f frame) bool {
		return f.str("action") == ActionInfo && f.str("what") == NoteRead && f.num("seq") == seq
	}
}

// readOn sends a read note from c and waits until watcher has it relayed: the
// cursor is written before the note is relayed, so it is stored by then.
func readOn(t *testing.T, c, watcher *liveConn, topic string, seq int64) {
	t.Helper()
	c.send(frame{"action": ActionNote, "topic": topic, "what": NoteRead, "seq": seq})
	if _, ok := watcher.waitFor(readNoteAt(seq), 5*time.Second); !ok {
		t.Fatalf("the read of %d was not relayed", seq)
	}
}

func historyOf(t *testing.T, c *liveConn, topic string) frame {
	t.Helper()
	reply := c.request(frame{"action": ActionGet, "topic": topic})
	if reply.num("code") != CodeOK {
		t.Fatalf("get %s: %v", topic, reply)
	}
	return reply
}

func TestReadSeq_DirectChatOwnReadPosition(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(profileName(strict), func(t *testing.T) {
			ls := newProfileServer(t, strict)
			peer := ls.connect("rs-peer")
			phone := ls.connectDevice("rs-me", "phone")
			me := phone.uid
			subscribe(t, me, peer)
			var seqs []int64
			for i := 0; i < 3; i++ {
				ack := peer.request(frame{"action": ActionPub, "topic": me, "content": fmt.Sprintf("m%d", i), "contentType": "text/plain"})
				if ack.num("code") != CodeOK {
					t.Fatalf("pub: %v", ack)
				}
				seqs = append(seqs, ack.num("seq"))
			}
			last := seqs[2]

			readOn(t, phone, peer, peer.uid, seqs[1])
			tablet := ls.connectDevice("rs-me", "tablet")
			reply := historyOf(t, tablet, peer.uid)
			if _, present := reply["readSeq"]; !strict {
				if present {
					t.Fatalf("legacy: the get reply carries readSeq: %v", reply)
				}
				return
			}
			if got := reply.num("readSeq"); got != seqs[1] {
				t.Fatalf("a new device got readSeq %d, want %d: %v", got, seqs[1], reply)
			}

			// Only forward: a late read of an older message changes nothing.
			readOn(t, phone, peer, peer.uid, seqs[0])
			if got := historyOf(t, tablet, peer.uid).num("readSeq"); got != seqs[1] {
				t.Fatalf("after an older read readSeq is %d, want %d", got, seqs[1])
			}

			// A read past the end of the conversation is held at its last seq.
			readOn(t, phone, peer, peer.uid, last+100)
			reply = historyOf(t, tablet, peer.uid)
			if got := reply.num("readSeq"); got != last {
				t.Fatalf("after a read past the end readSeq is %d, want %d", got, last)
			}

			// The same conversation asked for by its p2p_ topic.
			msgs, _ := reply["messages"].([]any)
			if len(msgs) == 0 {
				t.Fatalf("no messages: %v", reply)
			}
			p2p := frame(msgs[0].(map[string]any)).str("topic")
			if got := historyOf(t, tablet, p2p).num("readSeq"); got != last {
				t.Fatalf("get by %s: readSeq %d, want %d", p2p, got, last)
			}

			// It is the caller's own position: the peer, who read nothing, has none.
			if r := historyOf(t, peer, me); r["readSeq"] != nil {
				t.Fatalf("the peer got readSeq %v", r["readSeq"])
			}
		})
	}
}

// A conversation nobody has read has no readSeq, under either profile.
func TestReadSeq_OmittedWhenNothingRead(t *testing.T) {
	ls := newProfileServer(t, true)
	peer := ls.connect("rs0-peer")
	me := ls.connect("rs0-me")
	if ack := peer.request(frame{"action": ActionPub, "topic": me.uid, "content": "hi", "contentType": "text/plain"}); ack.num("code") != CodeOK {
		t.Fatalf("pub: %v", ack)
	}
	if r := historyOf(t, me, peer.uid); r["readSeq"] != nil {
		t.Fatalf("readSeq %v before any read", r["readSeq"])
	}
}

// A group's own position comes from the read-receipt cursors: with read
// receipts off nothing is stored and there is no readSeq; with them on it is
// the caller's entry of readCursors.
func TestReadSeq_GroupFollowsReadReceipts(t *testing.T) {
	for _, receipts := range []bool{false, true} {
		t.Run(fmt.Sprintf("receipts=%v", receipts), func(t *testing.T) {
			rc := defaultRuntimeConfig()
			rc.ReadReceiptEnabled = receipts
			setRuntimeConfig(rc)
			defer setRuntimeConfig(defaultRuntimeConfig())

			ls := newProfileServer(t, true)
			owner := ls.connect("rsg-owner")
			phone := ls.connectDevice("rsg-me", "phone")
			group, err := ls.store.CreateGroup(owner.uid, "g", []string{phone.uid})
			if err != nil {
				t.Fatal(err)
			}
			subscribe(t, group, owner, phone)
			ack := owner.request(frame{"action": ActionPub, "topic": group, "content": "g1", "contentType": "text/plain"})
			if ack.num("code") != CodeOK {
				t.Fatalf("pub: %v", ack)
			}
			readOn(t, phone, owner, group, ack.num("seq"))

			tablet := ls.connectDevice("rsg-me", "tablet")
			reply := historyOf(t, tablet, group)
			if !receipts {
				if reply["readSeq"] != nil || reply["readCursors"] != nil {
					t.Fatalf("read receipts off: %v", reply)
				}
				return
			}
			if got := reply.num("readSeq"); got != ack.num("seq") {
				t.Fatalf("readSeq %d, want %d: %v", got, ack.num("seq"), reply)
			}
			if r := historyOf(t, owner, group); r["readSeq"] != nil {
				t.Fatalf("the owner, who read nothing, got readSeq %v", r["readSeq"])
			}
		})
	}
}
