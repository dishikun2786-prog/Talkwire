// Package standalone runs the IM server without a host process. Everything a
// host would answer comes from an in-process configuration, loadable from a
// JSON file:
//
//	{
//	  "relations": "whitelist",
//	  "friends": [["alice", "bob"]],
//	  "profiles": {"alice": {"displayName": "Alice", "avatarUrl": "https://..."}},
//	  "runtimeConfig": {"maxMembers": 200},
//	  "e2eeConfig": {"enabled": true}
//	}
//
// relations is "allow-all" (the default: anyone may message anyone) or
// "whitelist" (only the listed pairs, in either order). There is no media
// storage to clean up. The host offers neither host.CallSessions nor
// host.GroupAI, so the call and group-AI modules cannot be installed.
package standalone

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"example.com/imkit/server/host"
)

// Relation policies.
const (
	AllowAll  = "allow-all"
	Whitelist = "whitelist"
)

// Profile is how a user is displayed to everyone.
type Profile struct {
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl"`
}

// Options is the in-process stand-in for a host.
type Options struct {
	Relations     string             `json:"relations"`
	Friends       [][2]string        `json:"friends"`
	Profiles      map[string]Profile `json:"profiles"`
	RuntimeConfig json.RawMessage    `json:"runtimeConfig"`
	E2eeConfig    json.RawMessage    `json:"e2eeConfig"`
}

// Host answers from Options. It implements host.Host.
type Host struct {
	allowAll bool
	friends  map[[2]string]bool
	profiles map[string]Profile
	runtime  json.RawMessage
	e2ee     json.RawMessage
}

var _ host.Host = (*Host)(nil)

// New validates the options and returns the host.
func New(o Options) (*Host, error) {
	h := &Host{friends: map[[2]string]bool{}, profiles: o.Profiles, runtime: o.RuntimeConfig, e2ee: o.E2eeConfig}
	switch o.Relations {
	case "", AllowAll:
		h.allowAll = true
		if len(o.Friends) > 0 {
			return nil, errors.New(`standalone: "friends" is only used with relations "whitelist"`)
		}
	case Whitelist:
		for _, p := range o.Friends {
			if p[0] == "" || p[1] == "" || p[0] == p[1] {
				return nil, fmt.Errorf("standalone: invalid friend pair %q", p)
			}
			h.friends[pair(p[0], p[1])] = true
		}
	default:
		return nil, fmt.Errorf("standalone: relations must be %q or %q, got %q", AllowAll, Whitelist, o.Relations)
	}
	return h, nil
}

// Load reads Options from a JSON file. Unknown fields are an error, so a
// misspelled key cannot silently fall back to allow-all.
func Load(path string) (*Host, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var o Options
	if err := dec.Decode(&o); err != nil {
		return nil, fmt.Errorf("standalone: %s: %w", path, err)
	}
	return New(o)
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}

// AreFriends implements host.Relations.
func (h *Host) AreFriends(appUserA, appUserB string) (bool, error) {
	if h.allowAll {
		return appUserA != appUserB, nil
	}
	return h.friends[pair(appUserA, appUserB)], nil
}

// GetProfile implements host.Profiles. Users without a profile are displayed
// with empty fields, which the server replaces with its generic wording.
func (h *Host) GetProfile(_, subjectAppUserID string) (string, string, error) {
	p := h.profiles[subjectAppUserID]
	return p.DisplayName, p.AvatarURL, nil
}

// DeleteMedia implements host.Media. There is no media storage to clean up.
func (h *Host) DeleteMedia([]string) error { return nil }

// RuntimeConfig implements host.Config.
func (h *Host) RuntimeConfig() (json.RawMessage, error) { return doc(h.runtime) }

// E2eeConfig implements host.Config.
func (h *Host) E2eeConfig() (json.RawMessage, error) { return doc(h.e2ee) }

func doc(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, host.ErrNoConfig
	}
	return raw, nil
}
