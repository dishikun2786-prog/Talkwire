package main

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"example.com/imkit/server/push"
	"github.com/golang-jwt/jwt/v5"
)

// withStrict runs fn under the strict profile.
func withStrict(t *testing.T, fn func()) {
	t.Helper()
	prev := strictProtocol
	strictProtocol = true
	defer func() { strictProtocol = prev }()
	fn()
}

func TestLegacyProfileAddsNothing(t *testing.T) {
	if strictProtocol {
		t.Fatal("tests must start in the legacy profile")
	}
	if e := ErrorResponse("r1", ActionPub, CodeAuth, "not authenticated"); e.ErrCode != "" {
		t.Errorf("legacy error frame has errCode %q", e.ErrCode)
	}
	h := &Hub{vapidKey: "k"}
	if got := h.loginReply("r1", "uid_a"); !reflect.DeepEqual(got, &ServerMessage{Action: ActionLogin, Code: CodeOK, UID: "uid_a"}) {
		t.Errorf("legacy login reply %+v", got)
	}
}

func TestStrictLoginReply(t *testing.T) {
	withStrict(t, func() {
		h := &Hub{vapidKey: "BKey"}
		if _, err := installModules(h, nopHost{}, []string{"e2ee", "call"}, true); err != nil {
			t.Fatal(err)
		}
		h.pusher = push.NewDispatcher(map[string]push.Sender{push.WebPush: &fakeSender{}, push.FCM: &fakeSender{}})
		got := h.loginReply("r1", "uid_a")
		want := &ServerMessage{
			Id: "r1", Action: ActionLogin, Code: CodeOK, UID: "uid_a", Protocol: ProtocolVersion,
			Capabilities: []string{CapErrCode, CapReplyID, "module:e2ee", "module:call", "push:fcm", "push:webpush"},
			VapidKey:     "BKey",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("strict login reply\n got  %+v\n want %+v", got, want)
		}
		if e := ErrorResponse("r2", ActionPub, CodeBadReq, "message too long"); e.ErrCode != "message_too_long" {
			t.Errorf("strict error frame errCode %q", e.ErrCode)
		}
	})
}

func TestErrCodeFor(t *testing.T) {
	for _, c := range []struct {
		code   int
		reason string
		want   string
	}{
		{CodeAuth, "not authenticated", "not_authenticated"},
		{CodeBadReq, "群名称过长", "group_name_too_long"},
		{CodeBadReq, "unknown action: frobnicate", "unknown_action"},
		{CodeAuth, "not friends: uid_x", "not_friends"},
		{CodeNotEnabled, "module not enabled: call", "module_not_enabled"},
		{CodeConflict, "group_encrypted", "group_encrypted"},
		{CodeConflict, "insufficient_credits", "insufficient_credits"}, // a host reason
		{CodeInternal, "保存失败", "internal_error"},
		{CodeBadReq, "Some New Reason", "bad_request"},
		{CodeAuth, "?", "unauthorized"},
		{CodeNotFound, "gone", "gone"},
		{CodeNotFound, "Gone!", "not_found"},
		{CodeConflict, "two words", "conflict"},
		{CodeRateLimited, "slow down", "rate_limited"},
	} {
		if got := errCodeFor(c.code, c.reason); got != c.want {
			t.Errorf("errCodeFor(%d, %q) = %q, want %q", c.code, c.reason, got, c.want)
		}
	}
}

// TestEveryErrorReasonHasACode reads every ErrorResponse call in the package.
// A literal reason must have an errCode of its own (table, prefix, or already
// snake_case), except 500s, which are all internal_error. A reason that is a
// variable must be one of the known host-supplied ones.
func TestEveryErrorReasonHasACode(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	checked := 0
	variables := map[string]int{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 4 {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "ErrorResponse" {
				return true
			}
			checked++
			pos := fset.Position(call.Pos())
			internal := false
			if id, ok := call.Args[2].(*ast.Ident); ok && id.Name == "CodeInternal" {
				internal = true
			}
			switch r := call.Args[3].(type) {
			case *ast.BasicLit:
				reason, _ := strconv.Unquote(r.Value)
				if _, ok := errCodes[reason]; !ok && !snakeCase.MatchString(reason) && !internal {
					t.Errorf("%s: reason %q has no errCode", pos, reason)
				}
			case *ast.BinaryExpr:
				lit, ok := r.X.(*ast.BasicLit)
				prefix, _ := strconv.Unquote(lit.Value)
				found := false
				for _, p := range errCodePrefixes {
					found = found || p.prefix == prefix
				}
				if !ok || !found {
					t.Errorf("%s: reason prefix %q has no errCode", pos, prefix)
				}
			default:
				variables[string(src[fset.Position(r.Pos()).Offset:fset.Position(r.End()).Offset])]++
			}
			return true
		})
	}
	if checked < 200 {
		t.Errorf("found only %d ErrorResponse calls; the scan is missing files", checked)
	}
	// Reasons the host supplies for calls (CreateCallSession,
	// AnswerCallSession, UpgradeCallSession): snake_case codes, passed through.
	want := map[string]int{"reason": 2, "result.Reason": 1}
	if !reflect.DeepEqual(variables, want) {
		t.Errorf("variable reasons %v, want %v; check that each is a machine-readable code", variables, want)
	}
}

// R22: under the strict profile a presence query reports only users sharing
// a conversation with the caller.
func TestPresenceQueryScope(t *testing.T) {
	s := testStore(t)
	a, _ := s.EnsureUser("app-a")
	b, _ := s.EnsureUser("app-b")
	stranger, _ := s.EnsureUser("app-stranger")
	if _, err := s.EnsureP2PTopic(a, b); err != nil {
		t.Fatal(err)
	}
	h := &Hub{store: s, clients: map[string]*Client{}, presRev: map[string]int64{}}
	query := func() []string {
		c := &Client{hub: h, uid: a, send: make(chan []byte, 4)}
		h.handlePresQuery(c, &ClientMessage{Id: "q1", Action: ActionPres, Topics: []string{b, stranger}})
		var reply struct {
			Id     string `json:"id"`
			States []struct {
				UID string `json:"uid"`
			} `json:"states"`
		}
		if err := json.Unmarshal(<-c.send, &reply); err != nil {
			t.Fatal(err)
		}
		var uids []string
		for _, st := range reply.States {
			uids = append(uids, st.UID)
		}
		sort.Strings(uids)
		if strictProtocol && reply.Id != "q1" {
			t.Errorf("strict snapshot reply id %q, want q1", reply.Id)
		}
		return uids
	}
	if got, want := query(), sortedOf(b, stranger); !reflect.DeepEqual(got, want) {
		t.Errorf("legacy reports %v, want %v (the reference behaviour, F30)", got, want)
	}
	withStrict(t, func() {
		if got := query(); !reflect.DeepEqual(got, []string{b}) {
			t.Errorf("strict reports %v, want only the peer %s", got, b)
		}
	})
}

func sortedOf(s ...string) []string {
	sort.Strings(s)
	return s
}

func TestNewIdentityPolicy(t *testing.T) {
	refresh, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "alice", "type": "refresh", "role": "app_user", "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte("secret-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name     string
		cfg      Config
		accepted bool
	}{
		{"legacy", Config{Profile: ProfileLegacy}, true},
		{"strict", Config{Profile: ProfileStrict}, false},
		{"strict, migration switch", Config{Profile: ProfileStrict, AcceptLegacyTokens: true}, true},
	} {
		c.cfg.JWTSecret = "secret-0123456789"
		id, err := newIdentity(&c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := id.UserID(refresh); (err == nil) != c.accepted {
			t.Errorf("%s: refresh token accepted=%v, want %v", c.name, err == nil, c.accepted)
		}
	}
}
