package main

import (
	"fmt"
	"testing"
	"time"
)

// F64: a backlog of queued envelopes comes back in queue order, in pages of
// at most 500, each envelope once and only to its recipient.
func TestPopE2eEnvelopesOldestFirst(t *testing.T) {
	s := testStore(t)
	if err := s.InitTables(mustE2eeMigrations(t)); err != nil {
		t.Fatal(err)
	}
	const total = 520
	for i := 0; i < total; i++ {
		for _, to := range []string{"uid_bob", "uid_carol"} {
			if err := s.EnqueueE2eEnvelope("p2p_x", to, "uid_alice", "msg", fmt.Sprintf("%s-%d", to, i), 1, time.Hour); err != nil {
				t.Fatal(err)
			}
		}
	}

	var got []string
	for page := 0; ; page++ {
		popped, err := s.PopE2eEnvelopesForUID("uid_bob")
		if err != nil {
			t.Fatal(err)
		}
		if len(popped) > 500 {
			t.Fatalf("page %d has %d envelopes", page, len(popped))
		}
		if len(popped) == 0 {
			break
		}
		for _, e := range popped {
			got = append(got, e.Payload)
		}
	}
	if len(got) != total {
		t.Fatalf("got %d of %d envelopes", len(got), total)
	}
	for i, p := range got {
		if p != fmt.Sprintf("uid_bob-%d", i) {
			t.Fatalf("envelope %d is %q", i, p)
		}
	}

	carol, err := s.PopE2eEnvelopesForUID("uid_carol")
	if err != nil {
		t.Fatal(err)
	}
	if len(carol) != 500 || carol[0].Payload != "uid_carol-0" || carol[499].Payload != "uid_carol-499" {
		t.Fatalf("another recipient's queue: %d envelopes", len(carol))
	}
}
