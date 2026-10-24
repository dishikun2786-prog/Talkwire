// Package fakehost is a scriptable stand-in for the business backend ("host")
// that the IM server calls back into. It implements the IM -> host endpoints
// observed in the reference server and records every call so a transcript can
// assert on the host contract as well as on the wire protocol.
package fakehost

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"example.com/imkit/tools/conformance/token"
)

// Profile is what GET /api/v1/internal/im/profile returns.
type Profile struct {
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl"`
}

// Call is one request the IM server made to the host.
type Call struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   json.RawMessage   `json:"body,omitempty"`
	Status int               `json:"status"`
}

// Host holds the scripted state. All methods are safe for concurrent use.
type Host struct {
	secret string

	mu            sync.Mutex
	friends       map[[2]string]bool
	profiles      map[string]Profile
	runtimeConfig json.RawMessage
	e2eeConfig    json.RawMessage
	calls         []Call
}

// New returns an empty host that validates internal tokens with secret.
func New(secret string) *Host {
	h := &Host{secret: secret}
	h.Reset()
	return h
}

// Reset clears all state and the call log.
func (h *Host) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.friends = map[[2]string]bool{}
	h.profiles = map[string]Profile{}
	h.runtimeConfig = nil
	h.e2eeConfig = nil
	h.calls = nil
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// SetFriends records whether two host users have an accepted relationship.
func (h *Host) SetFriends(a, b string, yes bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if yes {
		h.friends[pair(a, b)] = true
	} else {
		delete(h.friends, pair(a, b))
	}
}

// SetProfile sets the name and avatar returned for a user.
func (h *Host) SetProfile(user string, p Profile) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.profiles[user] = p
}

// SetRuntimeConfig makes GET runtime-config answer with raw; nil answers 404,
// which the reference server treats as "keep the built-in defaults".
func (h *Host) SetRuntimeConfig(raw json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runtimeConfig = raw
}

// SetE2eeConfig does the same for GET e2ee-config.
func (h *Host) SetE2eeConfig(raw json.RawMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.e2eeConfig = raw
}

// TakeCalls returns the calls recorded since the previous TakeCalls and clears
// the log. Periodic config refreshes are left out: their timing depends on the
// clock, not on the scenario.
func (h *Host) TakeCalls() []Call {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.calls
	h.calls = nil
	return out
}

func isPeriodic(path string) bool {
	return strings.HasSuffix(path, "/runtime-config") || strings.HasSuffix(path, "/e2ee-config")
}

// ServeHTTP implements the host side of the contract.
func (h *Host) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	status, resp := h.handle(r, body)
	if !isPeriodic(r.URL.Path) {
		q := map[string]string{}
		keys := make([]string, 0, len(r.URL.Query()))
		for k := range r.URL.Query() {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			q[k] = r.URL.Query().Get(k)
		}
		if len(q) == 0 {
			q = nil
		}
		c := Call{Method: r.Method, Path: r.URL.Path, Query: q, Status: status}
		if len(body) > 0 && json.Valid(body) {
			c.Body = json.RawMessage(body)
		}
		h.mu.Lock()
		h.calls = append(h.calls, c)
		h.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *Host) handle(r *http.Request, body []byte) (int, any) {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return http.StatusUnauthorized, map[string]string{"error": "missing token"}
	}
	claims, err := token.Verify(h.secret, strings.TrimPrefix(auth, "Bearer "))
	if err != nil || claims["role"] != "internal_service" {
		return http.StatusUnauthorized, map[string]string{"error": "internal_service role required"}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	q := r.URL.Query()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/internal/im/friendship":
		return http.StatusOK, map[string]bool{"friends": h.friends[pair(q.Get("a"), q.Get("b"))]}
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/internal/im/profile":
		return http.StatusOK, h.profiles[q.Get("sender")]
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/internal/im/runtime-config":
		if h.runtimeConfig == nil {
			return http.StatusNotFound, map[string]string{"error": "not configured"}
		}
		return http.StatusOK, h.runtimeConfig
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/internal/im/e2ee-config":
		if h.e2eeConfig == nil {
			return http.StatusNotFound, map[string]string{"error": "not configured"}
		}
		return http.StatusOK, h.e2eeConfig
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/im/delete-media":
		return http.StatusOK, map[string]bool{"ok": true}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/im/calls/create":
		// The host, not the IM server, decides whether a call may start.
		var req struct {
			Caller string `json:"callerAppUserId"`
			Callee string `json:"calleeAppUserId"`
		}
		_ = json.Unmarshal(body, &req)
		if !h.friends[pair(req.Caller, req.Callee)] {
			return http.StatusOK, map[string]any{"ok": false, "reason": "not_friends"}
		}
		return http.StatusOK, map[string]any{"ok": true}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/im/calls/answer":
		// Fixed values so transcripts are stable; the two tokens differ so a
		// swap between caller and callee would show up as a diff.
		return http.StatusOK, map[string]any{
			"ok": true, "channelId": "fake-channel", "callerToken": "fake-rtc-caller",
			"calleeToken": "fake-rtc-callee", "videoQuality": "sd", "denoiseEnabled": true, "appId": "fake-app",
		}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/im/calls/settle":
		return http.StatusOK, map[string]any{"ok": true}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/im/calls/upgrade":
		return http.StatusOK, map[string]any{"ok": true, "videoQuality": "hd"}
	case r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/im/group-ai-qa":
		// A question containing "quota" is refused, to exercise the fallback text.
		var req struct {
			Question string `json:"question"`
		}
		_ = json.Unmarshal(body, &req)
		if strings.Contains(req.Question, "quota") {
			return http.StatusOK, map[string]any{"ok": false, "reason": "quota_exceeded"}
		}
		return http.StatusOK, map[string]any{"ok": true, "answer": "fake answer"}
	}
	return http.StatusNotFound, map[string]string{"error": "fakehost: endpoint not scripted"}
}
