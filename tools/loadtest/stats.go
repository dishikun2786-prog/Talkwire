package main

import (
	"fmt"
	"io"
	"sort"
	"sync"
	"time"
)

type stats struct {
	mu sync.Mutex

	logins, acks, directs, groups []time.Duration

	ConnectFailed, LoginFailed, GroupsFailed, UnexpectedClose int
	Online, Groups                                            int
	Sent, WriteFailed                                         int
	AckFailed                                                 map[int]int
	expected, received, receivedGroup, receivedDirect         int
	pendingAcks                                               int
	notes                                                     map[string]int
	ConnectSeconds, SendSeconds                               float64
}

func newStats() *stats { return &stats{AckFailed: map[int]int{}, notes: map[string]int{}} }

func (s *stats) count(n *int) {
	s.mu.Lock()
	*n++
	s.mu.Unlock()
}

// note keeps one example of each distinct error, with how often it happened.
func (s *stats) note(msg string) {
	s.mu.Lock()
	if len(s.notes) < 20 || s.notes[msg] > 0 {
		s.notes[msg]++
	}
	s.mu.Unlock()
}

func (s *stats) login(d time.Duration) {
	s.mu.Lock()
	s.logins = append(s.logins, d)
	s.mu.Unlock()
}

func (s *stats) sent(expect int) {
	s.mu.Lock()
	s.Sent++
	s.pendingAcks++
	s.expected += expect
	s.mu.Unlock()
}

func (s *stats) writeFailed(expect int) {
	s.mu.Lock()
	s.WriteFailed++
	s.pendingAcks--
	s.expected -= expect
	s.mu.Unlock()
}

func (s *stats) ack(d time.Duration) {
	s.mu.Lock()
	s.acks = append(s.acks, d)
	s.pendingAcks--
	s.mu.Unlock()
}

// ackFailed: the server refused the message, so nobody should get it.
func (s *stats) ackFailed(code, expect int) {
	s.mu.Lock()
	s.AckFailed[code]++
	s.pendingAcks--
	s.expected -= expect
	s.mu.Unlock()
}

func (s *stats) delivered(group bool, d time.Duration) {
	s.mu.Lock()
	s.received++
	if group {
		s.receivedGroup++
		s.groups = append(s.groups, d)
	} else {
		s.receivedDirect++
		s.directs = append(s.directs, d)
	}
	s.mu.Unlock()
}

func (s *stats) settled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingAcks == 0 && s.received >= s.expected
}

// ------------------------------------------------------------------ report

type latency struct {
	Count int     `json:"count"`
	P50   float64 `json:"p50Ms"`
	P95   float64 `json:"p95Ms"`
	P99   float64 `json:"p99Ms"`
	Max   float64 `json:"maxMs"`
}

func summarize(ds []time.Duration) latency {
	if len(ds) == 0 {
		return latency{}
	}
	sorted := append([]time.Duration(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) float64 {
		i := int(q*float64(len(sorted))+0.5) - 1
		if i < 0 {
			i = 0
		}
		if i >= len(sorted) {
			i = len(sorted) - 1
		}
		return float64(sorted[i].Microseconds()) / 1000
	}
	return latency{Count: len(sorted), P50: at(0.50), P95: at(0.95), P99: at(0.99), Max: float64(sorted[len(sorted)-1].Microseconds()) / 1000}
}

type report struct {
	Users           int            `json:"users"`
	Online          int            `json:"online"`
	ConnectFailed   int            `json:"connectFailed"`
	LoginFailed     int            `json:"loginFailed"`
	Groups          int            `json:"groups"`
	GroupSize       int            `json:"groupSize"`
	GroupsFailed    int            `json:"groupsFailed"`
	RatePerUser     float64        `json:"ratePerUser"`
	ConnectSeconds  float64        `json:"connectSeconds"`
	SendSeconds     float64        `json:"sendSeconds"`
	Sent            int            `json:"sent"`
	SentPerSecond   float64        `json:"sentPerSecond"`
	WriteFailed     int            `json:"writeFailed"`
	AckFailed       map[int]int    `json:"ackFailedByCode"`
	AcksMissing     int            `json:"acksMissing"`
	Expected        int            `json:"expectedDeliveries"`
	Received        int            `json:"receivedDeliveries"`
	DeliveredPerSec float64        `json:"deliveredPerSecond"`
	Lost            int            `json:"lostDeliveries"`
	UnexpectedClose int            `json:"unexpectedClose"`
	Login           latency        `json:"login"`
	Ack             latency        `json:"ack"`
	Direct          latency        `json:"directDelivery"`
	Group           latency        `json:"groupDelivery"`
	Errors          map[string]int `json:"errors,omitempty"`
}

func (s *stats) report(o options) *report {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &report{
		Users: o.users, Online: s.Online, ConnectFailed: s.ConnectFailed, LoginFailed: s.LoginFailed,
		Groups: s.Groups, GroupSize: o.groupSize, GroupsFailed: s.GroupsFailed, RatePerUser: o.rate,
		ConnectSeconds: s.ConnectSeconds, SendSeconds: s.SendSeconds, Sent: s.Sent, WriteFailed: s.WriteFailed,
		AckFailed: s.AckFailed, AcksMissing: s.pendingAcks, Expected: s.expected, Received: s.received,
		UnexpectedClose: s.UnexpectedClose,
		Login:           summarize(s.logins), Ack: summarize(s.acks), Direct: summarize(s.directs), Group: summarize(s.groups),
		Errors: s.notes,
	}
	if s.SendSeconds > 0 {
		r.SentPerSecond = float64(s.Sent) / s.SendSeconds
		r.DeliveredPerSec = float64(s.received) / s.SendSeconds
	}
	if s.expected > s.received {
		r.Lost = s.expected - s.received
	}
	return r
}

// passed: everyone got in, every message was answered, and no more than
// maxLoss of the deliveries went missing.
func (r *report) passed(maxLoss float64) bool {
	if r.ConnectFailed+r.LoginFailed+r.GroupsFailed+r.AcksMissing+r.UnexpectedClose > 0 {
		return false
	}
	if r.Expected == 0 {
		return false
	}
	return float64(r.Lost)/float64(r.Expected) <= maxLoss
}

func (r *report) print(w io.Writer) {
	fmt.Fprintf(w, "users        %d online of %d (connect failed %d, login failed %d) in %.1fs\n", r.Online, r.Users, r.ConnectFailed, r.LoginFailed, r.ConnectSeconds)
	fmt.Fprintf(w, "groups       %d of %d members (failed %d)\n", r.Groups, r.GroupSize, r.GroupsFailed)
	fmt.Fprintf(w, "sent         %d in %.1fs (%.0f/s, %.2f per user per second); write failed %d; refused %v; unanswered %d\n",
		r.Sent, r.SendSeconds, r.SentPerSecond, r.RatePerUser, r.WriteFailed, r.AckFailed, r.AcksMissing)
	fmt.Fprintf(w, "deliveries   %d of %d (%.0f/s); lost %d\n", r.Received, r.Expected, r.DeliveredPerSec, r.Lost)
	fmt.Fprintf(w, "closed early %d\n", r.UnexpectedClose)
	line := func(name string, l latency) {
		fmt.Fprintf(w, "%-12s n=%-7d p50 %7.1fms  p95 %7.1fms  p99 %7.1fms  max %7.1fms\n", name, l.Count, l.P50, l.P95, l.P99, l.Max)
	}
	line("login", r.Login)
	line("ack", r.Ack)
	line("direct", r.Direct)
	line("group", r.Group)
	for msg, n := range r.Errors {
		fmt.Fprintf(w, "error ×%d     %s\n", n, msg)
	}
}
