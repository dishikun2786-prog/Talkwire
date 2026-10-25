package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestR13_InternalEndpointsRejectBadTokens: every internal endpoint that
// main.go registers under /api/v1/internal/ refuses a request whose token is
// missing, not a JWT, an app user's access token, expired, signed with
// another secret, or of another role. A valid internal_service token gets
// past the check (whatever the endpoint then makes of the request), so the
// 401s come from the token and nothing else.
func TestR13_InternalEndpointsRejectBadTokens(t *testing.T) {
	ls := newLiveServer(t, liveOptions{})
	h := NewWebSocketHandler(ls.hub)

	sign := func(secret, role string, exp time.Time) string {
		s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"role": role, "exp": exp.Unix()}).SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	later := time.Now().Add(time.Minute)
	bad := []struct{ name, token string }{
		{"no token", ""},
		{"not a JWT", "nonsense"},
		{"app user token", accessToken("r13-user")},
		{"expired", sign(liveSecret, "internal_service", time.Now().Add(-time.Minute))},
		{"another secret", sign("another-secret-0123456789", "internal_service", later)},
		{"another role", sign(liveSecret, "admin", later)},
	}
	valid := sign(liveSecret, "internal_service", later)

	// Kept in step with main.go: an endpoint added there without a row here
	// is caught by the count below.
	endpoints := []struct {
		method, path string
		handle       http.HandlerFunc
	}{
		{http.MethodPost, "/api/v1/internal/im/contact-sync", h.InternalContactSync},
		{http.MethodPost, "/api/v1/internal/im/media-ref-count", h.InternalMediaRefCount},
		{http.MethodPost, "/api/v1/internal/im/topic-members-resolve", h.InternalTopicMembersResolve},
		{http.MethodPost, "/api/v1/internal/im/custom-broadcast", h.InternalCustomBroadcast},
		{http.MethodPost, "/api/v1/internal/im/red-packet-broadcast", h.InternalRedPacketBroadcast},
		{http.MethodPost, "/api/v1/internal/im/call-broadcast", h.InternalCallBroadcast},
		{http.MethodGet, "/api/v1/internal/admin/im/topics", h.InternalAdminTopics},
		{http.MethodGet, "/api/v1/internal/admin/im/topic", h.InternalAdminTopic},
		{http.MethodGet, "/api/v1/internal/admin/im/messages", h.InternalAdminMessages},
		{http.MethodPost, "/api/v1/internal/admin/im/clear", h.InternalAdminClearTopic},
	}
	if want := strings.Count(mainSource(t), `mux.HandleFunc("/api/v1/internal/`); len(endpoints) != want {
		t.Fatalf("main.go registers %d internal endpoints, this test covers %d", want, len(endpoints))
	}

	call := func(method, path, token string, handle http.HandlerFunc) int {
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{}`)
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(method, path, body)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		handle(rec, req)
		return rec.Code
	}
	for _, e := range endpoints {
		for _, b := range bad {
			if code := call(e.method, e.path, b.token, e.handle); code != http.StatusUnauthorized {
				t.Errorf("%s with %s: %d, want 401", e.path, b.name, code)
			}
		}
		if code := call(e.method, e.path, valid, e.handle); code == http.StatusUnauthorized {
			t.Errorf("%s with a valid token: 401", e.path)
		}
	}
}

// mainSource is main.go as it stands (tests run in the package directory).
func mainSource(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
