package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func internalToken(t *testing.T) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"role": "internal_service", "exp": time.Now().Add(time.Minute).Unix(),
	}).SignedString([]byte(liveSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// customBroadcast calls the endpoint and returns its status and JSON body.
func customBroadcast(t *testing.T, ls *liveServer, token, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/im/custom-broadcast", strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	NewWebSocketHandler(ls.hub).InternalCustomBroadcast(rec, req)
	var out map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func isCustomUpdate(f frame) bool { return f.str("action") == "custom_update" }

func TestCustomBroadcastToTopic(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	a, b, outsider := ls.connect("cb-a"), ls.connect("cb-b"), ls.connect("cb-out")
	created := a.request(frame{"action": "create_grp", "name": "cards", "members": []string{b.uid}})
	topic := created.str("topic")
	if created.num("code") != 201 || topic == "" {
		t.Fatalf("create_grp: %v", created)
	}
	if r := b.request(frame{"action": "sub", "topic": topic}); r.num("code") != CodeOK {
		t.Fatalf("sub: %v", r)
	}

	code, body := customBroadcast(t, ls, internalToken(t),
		`{"topic":"`+topic+`","kind":"red_packet","payload":{"id":"p1","left":2}}`)
	if code != http.StatusOK || body["ok"] != true {
		t.Fatalf("custom-broadcast: %d %v", code, body)
	}
	if _, ok := body["delivered"]; ok {
		t.Error("a topic broadcast cannot say whether anyone got it, so it must not claim to")
	}
	for _, c := range []*liveConn{a, b} {
		f, ok := c.waitFor(isCustomUpdate, 5*time.Second)
		if !ok {
			t.Fatalf("%s got no custom_update", c.uid)
		}
		extra, _ := f["extra"].(map[string]interface{})
		if f.str("topic") != topic || f.str("what") != "red_packet" || extra["id"] != "p1" || extra["left"] != float64(2) {
			t.Errorf("%s: frame %v", c.uid, f)
		}
		if len(f) != 4 {
			t.Errorf("%s: unexpected fields in %v", c.uid, f)
		}
	}
	if _, ok := outsider.waitFor(isCustomUpdate, 300*time.Millisecond); ok {
		t.Error("a user outside the conversation got its card update")
	}
}

func TestCustomBroadcastToUser(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	phone, tablet := ls.connectDevice("cb-u", "phone"), ls.connectDevice("cb-u", "tablet")
	other := ls.connect("cb-other")

	code, body := customBroadcast(t, ls, internalToken(t),
		`{"targetUid":"`+phone.uid+`","kind":"contact.request","payload":{"from":"x"}}`)
	if code != http.StatusOK || body["ok"] != true || body["delivered"] != true {
		t.Fatalf("custom-broadcast: %d %v", code, body)
	}
	for _, c := range []*liveConn{phone, tablet} {
		f, ok := c.waitFor(isCustomUpdate, 5*time.Second)
		if !ok {
			t.Fatalf("device of %s got no custom_update", c.uid)
		}
		if _, has := f["topic"]; has || f.str("what") != "contact.request" {
			t.Errorf("frame %v", f)
		}
	}
	if _, ok := other.waitFor(isCustomUpdate, 300*time.Millisecond); ok {
		t.Error("another user got the update")
	}

	nobody, _ := ls.store.EnsureUser("cb-offline")
	code, body = customBroadcast(t, ls, internalToken(t), `{"targetUid":"`+nobody+`","kind":"x","payload":1}`)
	if code != http.StatusOK || body["delivered"] != false {
		t.Fatalf("offline user: %d %v", code, body)
	}
}

func TestCustomBroadcastRejects(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	tok := internalToken(t)
	cases := []struct {
		name, token, body string
		want              int
	}{
		{"no token", "", `{"topic":"grp_x","kind":"k","payload":1}`, http.StatusUnauthorized},
		{"app user token", accessToken("cb-u"), `{"topic":"grp_x","kind":"k","payload":1}`, http.StatusUnauthorized},
		{"no target", tok, `{"kind":"k","payload":1}`, http.StatusBadRequest},
		{"both targets", tok, `{"topic":"grp_x","targetUid":"uid_aaaaaaaaaaa","kind":"k","payload":1}`, http.StatusBadRequest},
		{"no kind", tok, `{"topic":"grp_x","payload":1}`, http.StatusBadRequest},
		{"upper-case kind", tok, `{"topic":"grp_x","kind":"Red","payload":1}`, http.StatusBadRequest},
		{"kind too long", tok, `{"topic":"grp_x","kind":"` + strings.Repeat("k", 65) + `","payload":1}`, http.StatusBadRequest},
		{"no payload", tok, `{"topic":"grp_x","kind":"k"}`, http.StatusBadRequest},
		{"null payload", tok, `{"topic":"grp_x","kind":"k","payload":null}`, http.StatusBadRequest},
		{"not JSON", tok, `{`, http.StatusBadRequest},
		{"too large", tok, `{"topic":"grp_x","kind":"k","payload":"` + strings.Repeat("a", maxCustomBroadcastBody) + `"}`, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		if code, body := customBroadcast(t, ls, c.token, c.body); code != c.want {
			t.Errorf("%s: got %d %v, want %d", c.name, code, body, c.want)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/im/custom-broadcast", nil)
	rec := httptest.NewRecorder()
	NewWebSocketHandler(ls.hub).InternalCustomBroadcast(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}
