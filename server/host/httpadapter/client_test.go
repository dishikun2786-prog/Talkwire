package httpadapter

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The host's group AI answer: the answer on success; on a business failure
// the reason and, when the host words it itself, its message.
func TestAskGroupAiQaReadsTheHostsWording(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		answer, reason string
		ok             bool
	}{
		{"answered", `{"ok":true,"answer":"42"}`, "42", "", true},
		{"failed, default wording", `{"ok":false,"reason":"quota_exceeded"}`, "", "quota_exceeded", false},
		{"failed, the host's wording", `{"ok":false,"reason":"quota_exceeded","message":"Come back tomorrow"}`, "Come back tomorrow", "quota_exceeded", false},
		{"a message beside an answer is ignored", `{"ok":true,"answer":"42","message":"x"}`, "42", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/internal/im/group-ai-qa" || r.Header.Get("Authorization") == "" {
					http.Error(w, "unexpected", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			answer, ok, reason := New(srv.URL, "test-secret-at-least-32-characters").AskGroupAiQa("grp_x", "uid_a", "q", "owner", nil, nil)
			if answer != tc.answer || ok != tc.ok || reason != tc.reason {
				t.Errorf("got (%q, %v, %q), want (%q, %v, %q)", answer, ok, reason, tc.answer, tc.ok, tc.reason)
			}
		})
	}
}
