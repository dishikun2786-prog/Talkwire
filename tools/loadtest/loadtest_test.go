package main

import (
	"testing"
	"time"
)

func TestUIDMatchesProtocolVectors(t *testing.T) {
	for id, want := range map[string]string{
		"alice":                                "uid_K9gGyX8OAK8",
		"bob":                                  "uid_gbY32PzSxto",
		"00000000-0000-4000-8000-000000000001": "uid_EeWU9IGVjBA",
		"用户一":                                  "uid_7c5xGaxvxPw",
	} {
		if got := uid(id); got != want {
			t.Errorf("uid(%q) = %s, want %s", id, got, want)
		}
	}
}

func TestBodyCarriesSendTime(t *testing.T) {
	at := time.Unix(1_800_000_000, 123456789)
	got, ok := sentAt(body(at))
	if !ok || !got.Equal(at) {
		t.Fatalf("sentAt(body) = %v, %v", got, ok)
	}
	if _, ok := sentAt("someone else's message"); ok {
		t.Fatal("a message without the marker has no send time")
	}
}

func TestPercentiles(t *testing.T) {
	var ds []time.Duration
	for i := 1; i <= 100; i++ {
		ds = append(ds, time.Duration(i)*time.Millisecond)
	}
	l := summarize(ds)
	if l.Count != 100 || l.P50 != 50 || l.P95 != 95 || l.P99 != 99 || l.Max != 100 {
		t.Fatalf("summarize = %+v", l)
	}
	if (summarize(nil) != latency{}) {
		t.Fatal("no samples summarize to zeros")
	}
}

func TestPassed(t *testing.T) {
	ok := report{Expected: 100, Received: 100}
	if !ok.passed(0) {
		t.Fatal("no loss passes")
	}
	lossy := report{Expected: 100, Received: 98, Lost: 2}
	if lossy.passed(0.01) || !lossy.passed(0.02) {
		t.Fatal("loss is compared with max-loss")
	}
	if (&report{Expected: 10, Received: 10, LoginFailed: 1}).passed(1) {
		t.Fatal("a failed login fails the run")
	}
	if (&report{}).passed(1) {
		t.Fatal("a run that delivered nothing does not pass")
	}
}
