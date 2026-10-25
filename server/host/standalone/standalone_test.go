package standalone

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.com/imkit/server/host"
)

func TestAllowAll(t *testing.T) {
	h, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.AreFriends("a", "b"); !ok {
		t.Error("allow-all refused a pair")
	}
	if ok, _ := h.AreFriends("a", "a"); ok {
		t.Error("a user is not their own friend")
	}
}

func TestWhitelistIsUnordered(t *testing.T) {
	h, err := New(Options{Relations: Whitelist, Friends: [][2]string{{"b", "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range [][2]string{{"a", "b"}, {"b", "a"}} {
		if ok, _ := h.AreFriends(p[0], p[1]); !ok {
			t.Errorf("%v refused", p)
		}
	}
	if ok, _ := h.AreFriends("a", "c"); ok {
		t.Error("unlisted pair allowed")
	}
}

func TestInvalidOptions(t *testing.T) {
	for name, o := range map[string]Options{
		"unknown policy":       {Relations: "friends-of-friends"},
		"friends on allow-all": {Friends: [][2]string{{"a", "b"}}},
		"self pair":            {Relations: Whitelist, Friends: [][2]string{{"a", "a"}}},
		"empty id":             {Relations: Whitelist, Friends: [][2]string{{"a", ""}}},
	} {
		if _, err := New(o); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	// A typo must not silently fall back to allow-all.
	path := filepath.Join(t.TempDir(), "standalone.json")
	if err := os.WriteFile(path, []byte(`{"relation":"whitelist","friends":[["a","b"]]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("misspelled key accepted")
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "standalone.json")
	doc := `{"relations":"whitelist","friends":[["a","b"]],"profiles":{"a":{"displayName":"Alice","avatarUrl":"https://x/a.png"}},"runtimeConfig":{"maxMembers":3}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := h.AreFriends("b", "a"); !ok {
		t.Error("listed pair refused")
	}
	if name, avatar, _ := h.GetProfile("b", "a"); name != "Alice" || avatar != "https://x/a.png" {
		t.Errorf("profile = %q %q", name, avatar)
	}
	if raw, err := h.RuntimeConfig(); err != nil || string(raw) != `{"maxMembers":3}` {
		t.Errorf("runtime config = %s, %v", raw, err)
	}
	if _, err := h.E2eeConfig(); !errors.Is(err, host.ErrNoConfig) {
		t.Errorf("missing e2ee config: %v, want ErrNoConfig", err)
	}
}

func TestNoModuleCapabilities(t *testing.T) {
	// Calls need a host to bill them and issue media credentials; group AI
	// needs a host to run the assistant. Without those capabilities the
	// server does not install the modules at all.
	var h any = &Host{}
	if _, ok := h.(host.CallSessions); ok {
		t.Error("standalone host claims to manage call sessions")
	}
	if _, ok := h.(host.GroupAI); ok {
		t.Error("standalone host claims to answer group AI questions")
	}
}
