package main

import "github.com/dishikun2786-prog/Talkwire/server/host"

// uidScheme maps host user ids to wire uids. The host computes uids the same
// way, so this must match the host's scheme.
var uidScheme host.UIDs = host.SHA256UIDs{}

// IMUserID returns the wire uid for a host user id.
func IMUserID(appUserID string) string {
	return uidScheme.UID(appUserID)
}
