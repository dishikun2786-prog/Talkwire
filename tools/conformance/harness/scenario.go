// Package harness drives an IM server over real WebSockets from declarative
// scenarios, records what every connection received as a normalized
// transcript, and compares transcripts. It is used two ways: recording golden
// transcripts against the reference server, and checking a new server against
// them.
package harness

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dishikun2786-prog/Talkwire/tools/conformance/fakehost"
)

// Scenario is one scripted conversation with the server.
type Scenario struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Redlines    []string  `json:"redlines,omitempty"`
	Module      string    `json:"module,omitempty"` // empty = core protocol
	Users       []string  `json:"users"`
	Host        HostSetup `json:"host"`
	SettleMs    int       `json:"settleMs,omitempty"`
	Steps       []Step    `json:"steps"`
}

// HostSetup scripts the fake host before the server starts.
type HostSetup struct {
	Friends       [][2]string                 `json:"friends,omitempty"`
	Profiles      map[string]fakehost.Profile `json:"profiles,omitempty"`
	RuntimeConfig json.RawMessage             `json:"runtimeConfig,omitempty"`
}

// Step is one action. Do is one of:
//
//	connect  open a WebSocket as Conn
//	login    send login for User on Conn (Claims override token claims;
//	         Token sends a literal token instead)
//	send     send Msg on Conn and wait for its reply unless Await is "none"
//	raw      send Raw as a text frame on Conn without waiting
//	close    close Conn from the client side
//	wait     sleep Ms milliseconds
//	host     call the server's internal API as the host: POST Call with Body
//	friends  change a relationship in the fake host: Friends = [A, B, "on"|"off"]
//
// Strings in Msg and Body may use ${user:X}, ${uid:X}, ${p2p:X,Y} and any
// variable captured by an earlier step.
type Step struct {
	Do     string          `json:"do"`
	Conn   string          `json:"conn,omitempty"`
	User   string          `json:"user,omitempty"`
	Claims map[string]any  `json:"claims,omitempty"`
	Token  string          `json:"token,omitempty"`
	Msg    json.RawMessage `json:"msg,omitempty"`
	Raw    string          `json:"raw,omitempty"`
	Await  string          `json:"await,omitempty"`
	// AwaitMatch replaces the default reply matcher (by id) with field
	// equality, for replies correlated another way (e.g. forward results
	// carry reqId and a "type" instead of the request id).
	AwaitMatch map[string]any    `json:"awaitMatch,omitempty"`
	Capture    map[string]string `json:"capture,omitempty"`
	Ms         int               `json:"ms,omitempty"`
	Call       string            `json:"call,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	Friends    []string          `json:"friends,omitempty"`
	Note       string            `json:"note,omitempty"`
	// Optional lists frames the reference server delivers only some of the
	// time (a documented race). Each entry names a "conn" plus fields a frame
	// must equal; matching frames are kept in the transcript but not compared.
	Optional []map[string]any `json:"optional,omitempty"`
}

// optionalMatch reports whether a normalized frame on conn matches one of the
// step's Optional entries.
func (s Step) optionalMatch(conn string, frame any) bool {
	m, ok := frame.(map[string]any)
	if !ok {
		return false
	}
	for _, want := range s.Optional {
		if want["conn"] != conn {
			continue
		}
		hit := true
		for k, v := range want {
			if k == "conn" {
				continue
			}
			if fmt.Sprint(m[k]) != fmt.Sprint(v) {
				hit = false
				break
			}
		}
		if hit {
			return true
		}
	}
	return false
}

// AppUserID is the host user id a scenario user key maps to.
func AppUserID(key string) string { return "conf-" + key }

// IMUID mirrors the reference server's uid derivation: sha256, first 8 bytes,
// unpadded base64url, "uid_" prefix.
func IMUID(appUserID string) string {
	h := sha256.Sum256([]byte(appUserID))
	return "uid_" + base64.RawURLEncoding.EncodeToString(h[:8])
}

// P2PTopic mirrors the reference server's direct-chat topic derivation.
func P2PTopic(uidA, uidB string) string {
	pair := []string{uidA, uidB}
	sort.Strings(pair)
	h := sha256.Sum256([]byte(pair[0] + ":" + pair[1]))
	return "p2p_" + base64.RawURLEncoding.EncodeToString(h[:8])
}

// LoadScenarios reads every *.json under dir (recursively), sorted by path.
func LoadScenarios(dir string) ([]*Scenario, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".json") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var out []*Scenario
	seen := map[string]string{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var sc Scenario
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&sc); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if sc.Name == "" {
			return nil, fmt.Errorf("%s: scenario has no name", p)
		}
		if prev, dup := seen[sc.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate scenario name %q (also in %s)", p, sc.Name, prev)
		}
		seen[sc.Name] = p
		out = append(out, &sc)
	}
	return out, nil
}
