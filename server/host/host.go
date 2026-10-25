// Package host defines what the IM server needs from the business backend it
// serves (the "host"): who a user is, how host user ids map to wire uids, who
// may message whom, how a user is displayed, where media lives and the live
// configuration.
//
// Two implementations ship with the server: httpadapter calls a host over
// HTTP (the paths, timeouts and internal token of the reference deployment),
// and standalone runs without any host process.
package host

import (
	"encoding/json"
	"errors"
)

// Identity turns credentials into host user ids.
type Identity interface {
	// UserID validates an end-user credential and returns the host user id
	// it names.
	UserID(token string) (string, error)
	// CheckInternalToken validates the credential the host presents when it
	// calls the server's internal endpoints.
	CheckInternalToken(token string) error
}

// UIDs maps a host user id to the uid used on the wire. The mapping must be
// deterministic: the host computes the same uids on its side.
type UIDs interface {
	UID(appUserID string) string
}

// Relations decides who may talk to whom.
type Relations interface {
	// AreFriends reports whether two host users have an accepted
	// relationship. Direct chats and invitations require one.
	AreFriends(appUserA, appUserB string) (bool, error)
}

// Profiles resolves how one user is displayed to another.
type Profiles interface {
	// GetProfile returns how the viewer sees the subject: the viewer's remark
	// for the subject when set, else the subject's own display name, and the
	// subject's avatar URL.
	GetProfile(viewerAppUserID, subjectAppUserID string) (displayName, avatarURL string, err error)
}

// Media is the host's media storage.
type Media interface {
	// DeleteMedia permanently removes the blobs behind these file URLs. The
	// server calls it once no remaining message references a blob.
	DeleteMedia(urls []string) error
}

// ErrNoConfig is returned by a Config that has no document of that kind. The
// server then keeps its built-in defaults and stops asking.
var ErrNoConfig = errors.New("host: no configuration document")

// Config is the host's live configuration. The documents are returned as raw
// JSON; the server parses and validates them, so a host can never put the
// server into a state its own defaults would not allow.
type Config interface {
	// RuntimeConfig returns the group-chat limits document.
	RuntimeConfig() (json.RawMessage, error)
	// E2eeConfig returns the end-to-end encryption feature flags document.
	E2eeConfig() (json.RawMessage, error)
}

// Host is the core contract: everything the server needs with no optional
// module installed, apart from Identity and UIDs, which are configured
// separately because they do not depend on how the host is reached.
type Host interface {
	Relations
	Profiles
	Media
	Config
}
