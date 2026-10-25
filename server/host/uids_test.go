package host

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

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

// contract/host-api/uid-vectors.json pins the scheme for every implementation,
// the Node host SDK and hosts in other languages included.
func TestSHA256UIDsMatchesTheContractVectors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contract", "host-api", "uid-vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Vectors []struct {
			HostUserID string `json:"hostUserId"`
			UID        string `json:"uid"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Vectors) == 0 {
		t.Fatal("no vectors")
	}
	for _, v := range doc.Vectors {
		if got := (SHA256UIDs{}).UID(v.HostUserID); got != v.UID {
			t.Errorf("UID(%q) = %q, want %q", v.HostUserID, got, v.UID)
		}
	}
}
