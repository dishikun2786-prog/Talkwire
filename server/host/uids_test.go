package host

import "testing"

// The host computes the same uids on its side, so the scheme must never drift.
// The second pair is a real account of the reference deployment, recorded in
// that project's own notes together with its uid.
func TestSHA256UIDsMatchesTheReferenceScheme(t *testing.T) {
	for appUserID, want := range map[string]string{
		"alice":                                "uid_K9gGyX8OAK8",
		"e0157dae-8524-410c-a3c8-87c0952312e5": "uid_osqFJdY4ACQ",
	} {
		if got := (SHA256UIDs{}).UID(appUserID); got != want {
			t.Errorf("UID(%q) = %q, want %q", appUserID, got, want)
		}
	}
}
