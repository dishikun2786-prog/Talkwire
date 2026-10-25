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
