package host

import (
	"crypto/sha256"
	"encoding/base64"
)

// SHA256UIDs is the uid scheme of the reference deployment: "uid_" followed
// by the first 8 bytes of SHA-256(appUserID), base64url without padding
// (11 characters). The host computes uids the same way, so changing the
// scheme breaks every stored uid.
type SHA256UIDs struct{}

// UID implements UIDs.
func (SHA256UIDs) UID(appUserID string) string {
	h := sha256.Sum256([]byte(appUserID))
	bare := base64.RawURLEncoding.EncodeToString(h[:8])
	return "uid_" + bare
}
