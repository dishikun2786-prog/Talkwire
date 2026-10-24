package harness

import "testing"

// These vectors are published in contract/protocol/spec/protocol-v1.md §5.
// They were computed with an independent Python implementation; this test
// keeps the harness (and therefore every recorded transcript) in agreement
// with the spec.
func TestIMUIDVectors(t *testing.T) {
	cases := map[string]string{
		"alice":                                "uid_K9gGyX8OAK8",
		"bob":                                  "uid_gbY32PzSxto",
		"00000000-0000-4000-8000-000000000001": "uid_EeWU9IGVjBA",
		"用户一":                                  "uid_7c5xGaxvxPw",
	}
	for in, want := range cases {
		if got := IMUID(in); got != want {
			t.Errorf("IMUID(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestP2PTopicVectors(t *testing.T) {
	alice, bob := IMUID("alice"), IMUID("bob")
	for _, pair := range [][2]string{{alice, bob}, {bob, alice}} {
		if got := P2PTopic(pair[0], pair[1]); got != "p2p_E5DsdYCpVC8" {
			t.Errorf("P2PTopic(%s, %s) = %s, want p2p_E5DsdYCpVC8", pair[0], pair[1], got)
		}
	}
	if got := P2PTopic(IMUID("00000000-0000-4000-8000-000000000001"), IMUID("用户一")); got != "p2p_VVXxwJ61Kvw" {
		t.Errorf("P2PTopic(uuid, 用户一) = %s, want p2p_VVXxwJ61Kvw", got)
	}
}
