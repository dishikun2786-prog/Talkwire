package main

import (
	"encoding/json"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

// mediaHost records DeleteMedia calls; it allows every relationship.
type mediaHost struct {
	mu      sync.Mutex
	deleted []string
}

func (m *mediaHost) AreFriends(string, string) (bool, error)           { return true, nil }
func (m *mediaHost) GetProfile(string, string) (string, string, error) { return "", "", nil }
func (m *mediaHost) DeleteMedia(urls []string) error {
	m.mu.Lock()
	m.deleted = append(m.deleted, urls...)
	m.mu.Unlock()
	return nil
}

func (m *mediaHost) waitDeleted(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		got := append([]string(nil), m.deleted...)
		m.mu.Unlock()
		if len(got) >= n || time.Now().After(deadline) {
			sort.Strings(got)
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestR6_RecallReclaimsVideoThumbnail(t *testing.T) {
	s := testStore(t)
	a, _ := s.EnsureUser("app-a")
	b, _ := s.EnsureUser("app-b")
	topic, err := s.EnsureP2PTopic(a, b)
	if err != nil {
		t.Fatal(err)
	}
	extra := json.RawMessage(`{"thumbUrl":"https://media/t1.jpg"}`)
	original, err := s.SaveMessage(topic, a, "", "video/mp4", "https://media/v1.mp4", "v1.mp4", extra, 3)
	if err != nil {
		t.Fatal(err)
	}
	forwarded, err := s.SaveMessage(topic, a, "", "video/mp4", "https://media/v1.mp4", "v1.mp4", extra, 3)
	if err != nil {
		t.Fatal(err)
	}

	host := &mediaHost{}
	h := &Hub{store: s, host: host, clients: map[string]map[string]*Client{}, topics: map[string]map[connKey]*Client{}}
	c := &Client{hub: h, uid: a, send: make(chan []byte, 8)}
	recall := func(seq int) {
		h.handleDel(c, &ClientMessage{Id: "d", Action: ActionDel, Topic: topic, Seq: seq})
		var reply ServerMessage
		json.Unmarshal(<-c.send, &reply)
		if reply.Code != CodeOK {
			t.Fatalf("recall of seq %d: %+v", seq, reply)
		}
	}

	recall(original)
	time.Sleep(300 * time.Millisecond) // the collection runs in goroutines
	if got := host.waitDeleted(t, 0); len(got) != 0 {
		t.Fatalf("deleted %v while the forwarded copy still uses them", got)
	}
	recall(forwarded)
	want := []string{"https://media/t1.jpg", "https://media/v1.mp4"}
	if got := host.waitDeleted(t, 2); !reflect.DeepEqual(got, want) {
		t.Errorf("deleted %v, want the file and its thumbnail %v", got, want)
	}
}

func TestSoftDeleteMessageReturnsEachURLOnce(t *testing.T) {
	s := testStore(t)
	a, _ := s.EnsureUser("app-a")
	b, _ := s.EnsureUser("app-b")
	topic, _ := s.EnsureP2PTopic(a, b)
	for _, c := range []struct {
		file, extra string
		want        []string
	}{
		{"", "", nil},
		{"https://m/f", "", []string{"https://m/f"}},
		{"https://m/f", `{"thumbUrl":"https://m/t"}`, []string{"https://m/f", "https://m/t"}},
		{"https://m/f", `{"thumbUrl":"https://m/f"}`, []string{"https://m/f"}},
	} {
		var extra json.RawMessage
		if c.extra != "" {
			extra = json.RawMessage(c.extra)
		}
		seq, err := s.SaveMessage(topic, a, "x", "text/plain", c.file, "", extra, 0)
		if err != nil {
			t.Fatal(err)
		}
		urls, ok, err := s.SoftDeleteMessage(topic, seq, a)
		if err != nil || !ok || !reflect.DeepEqual(urls, c.want) {
			t.Errorf("file %q extra %q: %v %v %v, want %v", c.file, c.extra, urls, ok, err, c.want)
		}
		if urls, ok, _ := s.SoftDeleteMessage(topic, seq, a); ok || urls != nil {
			t.Errorf("a second recall returned %v %v", urls, ok)
		}
	}
}
