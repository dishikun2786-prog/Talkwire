package main

import (
	"encoding/json"
	"testing"
	"time"
)

// The strict profile's fixes of defects D1, D2, D5, D7 and D8 (protocol
// specification section 18). Each test runs under both profiles: the legacy
// profile must keep the reference server's behaviour, the strict profile
// must not.

// newProfileServer starts a live server, with the given modules, under the
// legacy or the strict profile; the profile is restored once the server has
// stopped.
func newProfileServer(t *testing.T, strict bool, modules ...string) *liveServer {
	t.Helper()
	prev := strictProtocol
	strictProtocol = strict
	t.Cleanup(func() { strictProtocol = prev })
	return newLiveServer(t, liveOptions{modules: modules})
}

func profileName(strict bool) string {
	if strict {
		return ProfileStrict
	}
	return ProfileLegacy
}

// settle waits until each connection, in order, has every frame the server
// sent it so far: a round trip is answered after them. Settle the connection
// that acted first, so the frames its request caused are all sent.
func settle(t *testing.T, conns ...*liveConn) {
	t.Helper()
	for _, c := range conns {
		if reply := c.request(frame{"action": ActionGetGrps}); reply.num("code") != CodeOK {
			t.Fatalf("get_groups: %v", reply)
		}
	}
}

func grpSyncFrom(uid string) func(frame) bool {
	return func(f frame) bool { return f.str("action") == ActionGrpSync && f.str("from") == uid }
}

func subscribe(t *testing.T, topic string, conns ...*liveConn) {
	t.Helper()
	for _, c := range conns {
		if reply := c.request(frame{"action": ActionSub, "topic": topic}); reply.num("code") != CodeOK {
			t.Fatalf("sub %s: %v", topic, reply)
		}
	}
}

// D1 and D2 (findings F35, F36): leave_grp from someone who is not a member
// answered 200 and told every online member, and leave_grp on a one-to-one
// conversation removed the caller from it.
func TestD1D2_LeaveGrpChecksTopicAndMembership(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(profileName(strict), func(t *testing.T) {
			ls := newProfileServer(t, strict)
			owner := ls.connect("d1-owner")
			member := ls.connect("d1-member")
			stranger := ls.connect("d1-stranger")
			group, err := ls.store.CreateGroup(owner.uid, "g", []string{member.uid})
			if err != nil {
				t.Fatal(err)
			}
			subscribe(t, group, owner, member)

			reply := stranger.request(frame{"action": ActionLeaveGrp, "topic": group})
			settle(t, stranger, owner, member)
			syncs := len(member.frames(grpSyncFrom(stranger.uid)))
			if strict {
				if reply.num("code") != CodeAuth || reply.str("errCode") != "not_member" || syncs != 0 {
					t.Fatalf("D1: a stranger leaving got %v; members got %d grp_sync", reply, syncs)
				}
			} else if reply.num("code") != CodeOK || syncs != 1 {
				t.Fatalf("D1 legacy: a stranger leaving got %v; members got %d grp_sync, want the reference's 200 and 1", reply, syncs)
			}

			p2p, err := ls.store.EnsureP2PTopic(owner.uid, member.uid)
			if err != nil {
				t.Fatal(err)
			}
			reply = member.request(frame{"action": ActionLeaveGrp, "topic": p2p})
			still, err := ls.store.IsGroupMember(p2p, member.uid)
			if err != nil {
				t.Fatal(err)
			}
			if strict {
				if reply.num("code") != CodeBadReq || reply.str("errCode") != "not_a_group" || !still {
					t.Fatalf("D2: leave_grp on a one-to-one conversation got %v; still a member: %v", reply, still)
				}
			} else if reply.num("code") != CodeOK || still {
				t.Fatalf("D2 legacy: got %v, still a member: %v; the reference answers 200 and removes the caller", reply, still)
			}
		})
	}
}

// D5 (finding F24): a replayed group event carried the group name of when it
// was queued, so a group renamed meanwhile showed its old name.
func TestD5_ReplayedGroupEventCarriesTheCurrentName(t *testing.T) {
	s := testStore(t)
	owner, _ := s.EnsureUser("d5-owner")
	member, _ := s.EnsureUser("d5-member")
	topic, err := s.CreateGroup(owner, "old", []string{member})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnqueuePendingGroupEvent(topic, member, ActionGrpSync, "old", owner, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateGroupName(topic, "new"); err != nil {
		t.Fatal(err)
	}
	h := &Hub{store: s, clients: map[string]map[string]*Client{}, topics: map[string]map[connKey]*Client{}}
	replayedName := func() string {
		c := &Client{hub: h, uid: member, send: make(chan []byte, 4), subscribed: map[string]bool{}}
		h.deliverPendingGroupEvents(c)
		select {
		case raw := <-c.send:
			var f frame
			if err := json.Unmarshal(raw, &f); err != nil {
				t.Fatal(err)
			}
			return f.str("name")
		case <-time.After(5 * time.Second):
			t.Fatal("nothing replayed")
			return ""
		}
	}
	if name := replayedName(); name != "old" {
		t.Fatalf("legacy profile replayed name %q, want the queued %q", name, "old")
	}
	withStrict(t, func() {
		if name := replayedName(); name != "new" {
			t.Fatalf("strict profile replayed name %q, want the current %q", name, "new")
		}
		// A group that is gone keeps the name it was queued with.
		if name := h.replayedGroupName(PendingGroupEvent{Topic: "grp_gone", GroupName: "queued"}); name != "queued" {
			t.Fatalf("for a group that is gone: %q", name)
		}
	})
}

// D7 (finding F45): joining by scanning the group's code without approval
// told nobody: not the members, not the joiner's other devices, and the
// joining connection was not subscribed.
func TestD7_QrJoinIsAnnounced(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(profileName(strict), func(t *testing.T) {
			ls := newProfileServer(t, strict, "groupjoin")
			owner := ls.connect("d7-owner")
			member := ls.connect("d7-member")
			phone := ls.connectDevice("d7-joiner", "phone")
			web := ls.connectDevice("d7-joiner", "web")
			joiner := phone.uid
			group, err := ls.store.CreateGroup(owner.uid, "scan", []string{member.uid})
			if err != nil {
				t.Fatal(err)
			}
			subscribe(t, group, owner, member)
			qr, err := ls.store.MintGroupQrToken(group, owner.uid, time.Hour)
			if err != nil {
				t.Fatal(err)
			}

			reply := phone.request(frame{"action": ActionGroupJoin, "qrToken": qr.Token})
			if reply.num("code") != CodeOK || reply.str("joinStatus") != "joined" {
				t.Fatalf("group_join: %v", reply)
			}
			settle(t, phone, owner, member, web)
			ack := owner.request(frame{"action": ActionPub, "topic": group, "content": "welcome", "contentType": "text/plain"})
			if ack.num("code") != CodeOK {
				t.Fatalf("pub: %v", ack)
			}
			settle(t, owner, phone)
			got := map[string]int{
				"owner":  len(owner.frames(grpSyncFrom(joiner))),
				"member": len(member.frames(grpSyncFrom(joiner))),
				"web":    len(web.frames(grpSyncFrom(joiner))),
				"phone":  len(phone.frames(grpSyncFrom(joiner))),
				"data":   len(phone.frames(isData("welcome"))),
			}
			want := map[string]int{"owner": 0, "member": 0, "web": 0, "phone": 0, "data": 0}
			if strict {
				want = map[string]int{"owner": 1, "member": 1, "web": 1, "phone": 0, "data": 1}
			}
			for k, n := range want {
				if got[k] != n {
					t.Errorf("%s: got %d, want %d (all: %v)", k, got[k], n, got)
				}
			}
		})
	}
}

// D8 (finding F57): group_join_review marked the request handled before
// checking it, so a review naming the wrong group, or of a group that was
// full, used the request up: gone from the pending list, a later review got
// 409, and the applicant neither joined nor heard of it.
func TestD8_FailedReviewLeavesTheRequestPending(t *testing.T) {
	for _, strict := range []bool{false, true} {
		t.Run(profileName(strict), func(t *testing.T) {
			ls := newProfileServer(t, strict, "groupjoin")
			owner := ls.connect("d8-owner")
			applicant := ls.connect("d8-applicant")
			other, err := ls.store.CreateGroup(owner.uid, "other", nil)
			if err != nil {
				t.Fatal(err)
			}
			group, err := ls.store.CreateGroup(owner.uid, "g", nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := ls.store.UpsertGroupJoinSettings(group, true, owner.uid); err != nil {
				t.Fatal(err)
			}
			apply := func(message string) int64 {
				t.Helper()
				id, err := ls.store.CreateGroupJoinRequest(group, applicant.uid, message)
				if err != nil {
					t.Fatal(err)
				}
				return id
			}
			review := func(topic string, id int64, approve bool) frame {
				t.Helper()
				return owner.request(frame{"action": ActionGroupJoinReview, "topic": topic, "joinRequestId": id, "approve": approve})
			}
			statusOf := func(id int64) string {
				t.Helper()
				r, err := ls.store.GetGroupJoinRequest(id)
				if err != nil || r == nil {
					t.Fatalf("request %d: %v, %v", id, r, err)
				}
				return r.Status
			}
			isMember := func() bool {
				t.Helper()
				m, err := ls.store.IsGroupMember(group, applicant.uid)
				if err != nil {
					t.Fatal(err)
				}
				return m
			}

			// A review naming another group the owner manages.
			id := apply("first")
			if reply := review(other, id, true); reply.num("code") != CodeBadReq {
				t.Fatalf("review naming another group: %v", reply)
			}
			reply := review(group, id, true)
			// A handled request named under another group answers 409 in
			// both profiles: nothing more is revealed about it.
			handledElsewhere := func() {
				t.Helper()
				if reply := review(other, id, true); reply.num("code") != CodeConflict {
					t.Fatalf("a handled request under another group: %v", reply)
				}
			}
			if !strict {
				if reply.num("code") != CodeConflict || statusOf(id) != JoinRequestApproved || isMember() {
					t.Fatalf("legacy: the reference uses the request up (409, approved, not a member); got %v, %s, member %v", reply, statusOf(id), isMember())
				}
				handledElsewhere()
				return
			}
			if reply.num("code") != CodeOK || reply.str("joinStatus") != "approved" || statusOf(id) != JoinRequestApproved || !isMember() {
				t.Fatalf("the request was not left pending: %v, %s, member %v", reply, statusOf(id), isMember())
			}
			settle(t, owner, applicant)
			if n := len(applicant.frames(func(f frame) bool { return f.str("action") == ActionGroupJoinReviewed })); n != 1 {
				t.Fatalf("the applicant heard of the approval %d times", n)
			}
			if n := len(applicant.frames(grpSyncFrom(owner.uid))); n != 1 {
				t.Fatalf("the applicant got %d grp_sync", n)
			}
			handledElsewhere()

			// A group that is full: the request stays pending.
			if err := ls.store.RemoveGroupMember(group, applicant.uid); err != nil {
				t.Fatal(err)
			}
			full := defaultRuntimeConfig()
			full.MaxMembers = 1
			setRuntimeConfig(full)
			defer setRuntimeConfig(defaultRuntimeConfig())
			id = apply("second")
			if reply := review(group, id, true); reply.num("code") != CodeConflict || reply.str("errCode") != "group_full" {
				t.Fatalf("approving into a full group: %v", reply)
			}
			if statusOf(id) != JoinRequestPending || isMember() {
				t.Fatalf("after a review of a full group: %s, member %v", statusOf(id), isMember())
			}
			setRuntimeConfig(defaultRuntimeConfig())

			// A rejection is written as one.
			if reply := review(group, id, false); reply.num("code") != CodeOK || reply.str("joinStatus") != "rejected" {
				t.Fatalf("reject: %v", reply)
			}
			if statusOf(id) != JoinRequestRejected || isMember() {
				t.Fatalf("after a rejection: %s, member %v", statusOf(id), isMember())
			}
			if reply := review(group, id, true); reply.num("code") != CodeConflict || reply.str("errCode") != "join_request_handled" {
				t.Fatalf("a second review: %v", reply)
			}
		})
	}
}
