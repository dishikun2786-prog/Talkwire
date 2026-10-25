package main

import (
	"encoding/json"
	"testing"
)

// D9 (finding F59): the join flow's offline events were never queued. Under
// the strict profile an offline applicant learns the review result at the
// next login; the legacy profile keeps the reference behaviour (nothing).
func TestD9_JoinReviewQueuedForOfflineApplicant(t *testing.T) {
	s := testStore(t)
	owner, _ := s.EnsureUser("app-owner")
	applicant, _ := s.EnsureUser("app-applicant")
	topic, err := s.CreateGroup(owner, "G", nil)
	if err != nil {
		t.Fatal(err)
	}
	h := &Hub{store: s, clients: map[string]map[string]*Client{}, topics: map[string]map[connKey]*Client{}}
	queued := func() int {
		evs, err := s.GetPendingGroupEvents(applicant, "")
		if err != nil {
			t.Fatal(err)
		}
		return len(evs)
	}

	h.notifyRequesterOfReview(topic, applicant, "approved")
	if n := queued(); n != 0 {
		t.Fatalf("legacy profile queued %d event(s); the reference queues none", n)
	}
	withStrict(t, func() {
		h.notifyRequesterOfReview(topic, applicant, "approved")
	})
	if n := queued(); n != 1 {
		t.Fatalf("strict profile queued %d event(s), want 1", n)
	}
	c := &Client{hub: h, uid: applicant, send: make(chan []byte, 4)}
	h.deliverPendingGroupEvents(c)
	var m map[string]any
	if err := json.Unmarshal(<-c.send, &m); err != nil {
		t.Fatal(err)
	}
	if m["action"] != ActionGroupJoinReviewed || m["joinStatus"] != "approved" || m["topic"] != topic || m["eventId"] == nil {
		t.Errorf("replayed %v", m)
	}
}
