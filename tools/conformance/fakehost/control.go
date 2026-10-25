package fakehost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"example.com/imkit/tools/conformance/token"
)

// Control is what a scenario does to the host: script its answers and read
// back the calls the IM server made. Local drives the in-process fake host;
// Remote drives a host running elsewhere, in any language, through the
// conformance control API (tools/conformance/README.md), so the golden
// transcripts can check a real host implementation.
type Control interface {
	Reset() error
	SetFriends(a, b string, yes bool) error
	SetProfile(user string, p Profile) error
	SetRuntimeConfig(raw json.RawMessage) error
	SetE2eeConfig(raw json.RawMessage) error
	// TakeCalls returns the calls since the previous TakeCalls, leaving out
	// the periodic config refreshes, and clears them.
	TakeCalls() ([]Call, error)
}

// Local adapts the in-process fake host to Control.
func Local(h *Host) Control { return local{h} }

type local struct{ h *Host }

func (l local) Reset() error { l.h.Reset(); return nil }

func (l local) SetFriends(a, b string, yes bool) error { l.h.SetFriends(a, b, yes); return nil }

func (l local) SetProfile(user string, p Profile) error { l.h.SetProfile(user, p); return nil }

func (l local) SetRuntimeConfig(raw json.RawMessage) error { l.h.SetRuntimeConfig(raw); return nil }

func (l local) SetE2eeConfig(raw json.RawMessage) error { l.h.SetE2eeConfig(raw); return nil }

func (l local) TakeCalls() ([]Call, error) { return l.h.TakeCalls(), nil }

// ControlPrefix is where a host under test serves the control API.
const ControlPrefix = "/__conformance"

// Remote drives a host through the control API at base + ControlPrefix,
// authenticating with an internal_service token signed with secret.
func Remote(base, secret string) Control {
	return &remote{base: strings.TrimRight(base, "/") + ControlPrefix, secret: secret, client: &http.Client{Timeout: 10 * time.Second}}
}

type remote struct {
	base, secret string
	client       *http.Client
}

func (r *remote) post(path string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, r.base+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	tok, err := token.Internal(r.secret, time.Now())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("host control %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("host control %s: %d %s", path, resp.StatusCode, bytes.TrimSpace(raw))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("host control %s: %w", path, err)
		}
	}
	return nil
}

func (r *remote) Reset() error { return r.post("/reset", map[string]any{}, nil) }

func (r *remote) SetFriends(a, b string, yes bool) error {
	return r.post("/friends", map[string]any{"a": a, "b": b, "friends": yes}, nil)
}

func (r *remote) SetProfile(user string, p Profile) error {
	return r.post("/profile", map[string]any{"user": user, "displayName": p.DisplayName, "avatarUrl": p.AvatarURL}, nil)
}

// document is raw, or JSON null for "no document" (the host answers 404).
func document(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

func (r *remote) SetRuntimeConfig(raw json.RawMessage) error {
	return r.post("/runtime-config", map[string]any{"document": document(raw)}, nil)
}

func (r *remote) SetE2eeConfig(raw json.RawMessage) error {
	return r.post("/e2ee-config", map[string]any{"document": document(raw)}, nil)
}

func (r *remote) TakeCalls() ([]Call, error) {
	var out struct {
		Calls []Call `json:"calls"`
	}
	if err := r.post("/calls", map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Calls, nil
}
