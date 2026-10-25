package main

import "testing"

// Both spellings of the FCM key path are read; the deployed one wins when
// both are set.
func TestFCMKeyPathEnvNames(t *testing.T) {
	t.Setenv("DB_PASSWORD", "x")
	t.Setenv("JWT_SECRET", "y")
	for _, c := range []struct{ keyPath, path, want string }{
		{"", "", ""},
		{"/k.json", "", "/k.json"},
		{"", "/p.json", "/p.json"},
		{"/k.json", "/p.json", "/k.json"},
	} {
		t.Setenv("FCM_SERVICE_ACCOUNT_KEY_PATH", c.keyPath)
		t.Setenv("FCM_SERVICE_ACCOUNT_PATH", c.path)
		if got := LoadConfig().FCMServiceAccountPath; got != c.want {
			t.Errorf("KEY_PATH=%q PATH=%q: got %q, want %q", c.keyPath, c.path, got, c.want)
		}
	}
}

// A new deployment runs the strict profile; legacy is chosen explicitly.
func TestDefaultProfileIsStrict(t *testing.T) {
	t.Setenv("DB_PASSWORD", "x")
	t.Setenv("JWT_SECRET", "s")
	t.Setenv("IMKIT_PROTOCOL_PROFILE", "")
	if got := LoadConfig().Profile; got != ProfileStrict {
		t.Fatalf("default profile %q, want %q", got, ProfileStrict)
	}
	t.Setenv("IMKIT_PROTOCOL_PROFILE", ProfileLegacy)
	if got := LoadConfig().Profile; got != ProfileLegacy {
		t.Fatalf("profile %q, want %q", got, ProfileLegacy)
	}
}

// IM_HUB_CHAN_BUF and IM_PRESENCE_CHAN_BUF size the hub's queues; the
// reference server read them and kept its literals (F62).
func TestHubQueueSizesAreConfigurable(t *testing.T) {
	prevHub, prevPresence := hubChanBuf, presenceChanBuf
	defer func() { hubChanBuf, presenceChanBuf = prevHub, prevPresence }()
	hubChanBuf, presenceChanBuf = 7, 9
	h := NewHub(nil, nil, nil, nil)
	if cap(h.register) != 7 || cap(h.unregister) != 7 || cap(h.presenceCh) != 9 {
		t.Fatalf("queue sizes %d, %d, %d; want 7, 7, 9", cap(h.register), cap(h.unregister), cap(h.presenceCh))
	}
}
