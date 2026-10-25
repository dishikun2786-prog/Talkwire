package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sqlByFunction returns the SQL string literals of every function in the
// package's non-test files, by function name.
func sqlByFunction(t *testing.T) map[string][]string {
	t.Helper()
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	out := map[string][]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if s, err := strconv.Unquote(lit.Value); err == nil {
						out[fn.Name.Name] = append(out[fn.Name.Name], s)
					}
				}
				return true
			})
		}
	}
	return out
}

func anyContains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(strings.ToLower(s), strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

// R6: every query that reads messages skips recalled ones (the tombstone rows
// SoftDeleteMessage leaves), unless the function is a listed exception.
func TestR6_MessageReadsFilterRecalled(t *testing.T) {
	exceptions := map[string]string{
		"SaveMessageFullIdempotent": "the clientMsgId lookup must see a recalled message, or resending it would publish it again",
		"ClearTopicMessages":        "it deletes every row; deletedMessages counts tombstones too (F53, golden core/clear-media)",
		"CountFileRefs":             "tombstones have no file_url or extra, so counting all rows cannot pin a recalled message's media",
	}
	var readers []string
	for fn, sqls := range sqlByFunction(t) {
		reads := false
		for _, s := range sqls {
			u := strings.ToUpper(s)
			if strings.Contains(u, "SELECT") && strings.Contains(u, "IM_MESSAGES") {
				reads = true
			}
		}
		if !reads {
			continue
		}
		readers = append(readers, fn)
		_, excepted := exceptions[fn]
		filtered := anyContains(sqls, "recalled_at IS NULL")
		switch {
		case !filtered && !excepted:
			t.Errorf("%s reads im_messages without recalled_at IS NULL", fn)
		case filtered && excepted:
			t.Errorf("%s is listed as an exception but filters recalled rows; remove it from the list", fn)
		}
	}
	for fn := range exceptions {
		found := false
		for _, r := range readers {
			found = found || r == fn
		}
		if !found {
			t.Errorf("exception %s no longer reads im_messages; remove it", fn)
		}
	}
	if len(readers) < 8 {
		sort.Strings(readers)
		t.Errorf("found only %d message readers (%v); the scan is broken", len(readers), readers)
	}
}

// R6: the history page size is normalised in one function only.
func TestR6_HistoryLimitNormalizedOnce(t *testing.T) {
	for limit, want := range map[int]int{0: 50, -1: 50, 1: 1, 150: 150, 200: 200, 201: 50, 100000: 50} {
		if got := normalizeHistoryLimit(limit); got != want {
			t.Errorf("normalizeHistoryLimit(%d) = %d, want %d", limit, got, want)
		}
	}
	// No other function compares a limit against a literal ceiling: a second,
	// different ceiling is the bug this guards against.
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, _ := parser.ParseFile(fset, f, nil, 0)
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			// The admin bridge pages its listings with its own limit; it is
			// not the chat history page size.
			if !ok || fn.Name.Name == "normalizeHistoryLimit" || fn.Name.Name == "GetAdminTopics" || fn.Name.Name == "GetAdminMessages" {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				b, ok := n.(*ast.BinaryExpr)
				if !ok || (b.Op != token.GTR && b.Op != token.GEQ) {
					return true
				}
				if id, ok := b.X.(*ast.Ident); ok && strings.EqualFold(id.Name, "limit") {
					if _, lit := b.Y.(*ast.BasicLit); lit {
						t.Errorf("%s: %s compares limit with a literal; use normalizeHistoryLimit", fset.Position(b.Pos()), fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	// And the store honours a page size above the old store-side ceiling.
	s := testStore(t)
	a, _ := s.EnsureUser("app-a")
	b, _ := s.EnsureUser("app-b")
	topic, _ := s.EnsureP2PTopic(a, b)
	for i := 0; i < 150; i++ {
		if _, err := s.SaveMessage(topic, a, "m", "text/plain", "", "", nil, 0); err != nil {
			t.Fatal(err)
		}
	}
	if msgs, err := s.GetMessages(topic, 0, 150); err != nil || len(msgs) != 150 {
		t.Errorf("GetMessages(limit 150) returned %d messages, %v", len(msgs), err)
	}
	if msgs, err := s.GetMessagesAfter(topic, 0, 120); err != nil || len(msgs) != 120 {
		t.Errorf("GetMessagesAfter(limit 120) returned %d messages, %v", len(msgs), err)
	}
}

// R5: writing a member row never changes an existing role, and changing a
// role never touches the owner.
func TestR5_MemberWritesNeverDowngrade(t *testing.T) {
	// Every statement that inserts member rows must leave an existing row as
	// it is; every role update must exclude the owner, except the listed ones.
	insertExceptions := map[string]string{
		"CreateGroup": "the owner row of a group created in the same transaction; no row can exist yet",
	}
	updateExceptions := map[string]string{
		"TransferGroupOwnership": "the owner-gated transfer moves the owner role on purpose",
	}
	for fn, sqls := range sqlByFunction(t) {
		for _, s := range sqls {
			u := strings.Join(strings.Fields(strings.ToUpper(s)), " ")
			if strings.Contains(u, "INSERT INTO IM_TOPIC_MEMBERS") && !strings.Contains(u, "DO NOTHING") {
				if _, ok := insertExceptions[fn]; !ok || !strings.Contains(u, "VALUES") {
					t.Errorf("%s inserts member rows without ON CONFLICT DO NOTHING", fn)
				}
			}
			if strings.Contains(u, "UPDATE IM_TOPIC_MEMBERS SET ROLE") && !strings.Contains(u, "ROLE <>") {
				if _, ok := updateExceptions[fn]; !ok {
					t.Errorf("%s updates roles without excluding the owner", fn)
				}
			}
		}
	}

	s := testStore(t)
	owner, _ := s.EnsureUser("app-owner")
	admin, _ := s.EnsureUser("app-admin")
	topic, err := s.CreateGroup(owner, "G", []string{admin})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetGroupMemberRole(topic, admin, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	role := func(uid string) string {
		var r string
		if err := s.db.QueryRow(`SELECT role FROM im_topic_members WHERE topic = $1 AND uid = $2`, topic, uid).Scan(&r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	// Re-adding (an invite of someone already in the group) changes nothing.
	s.AddGroupMember(topic, admin)
	s.AddGroupMember(topic, owner)
	if role(admin) != RoleAdmin || role(owner) != RoleOwner {
		t.Errorf("after re-adding: admin is %s, owner is %s", role(admin), role(owner))
	}
	if err := s.SetGroupMemberRole(topic, owner, RoleMember); err == nil || role(owner) != RoleOwner {
		t.Errorf("the owner was demoted by a role change: %v, role %s", err, role(owner))
	}
	if err := s.SetGroupMemberRole(topic, admin, RoleOwner); err == nil {
		t.Error("a role change made someone owner")
	}
}

// R7, R8: unacknowledged clears and group events are dropped 30 days after
// they were queued, not before. The reapers take the time, so the test moves
// the clock instead of waiting.
func TestR7_PendingClearsExpireAfter30Days(t *testing.T) {
	s := testStore(t)
	a, _ := s.EnsureUser("app-a")
	if err := s.EnqueuePendingClear("p2p_x", a, "uid_x", 1, 5, "uid_x"); err != nil {
		t.Fatal(err)
	}
	expectQueued(t, "clear", func() int { l, _ := s.GetPendingClears(a); return len(l) },
		func(now time.Time) { s.ReapExpiredPendingClears(now) })
}

func TestR8_GroupEventsExpireAfter30Days(t *testing.T) {
	s := testStore(t)
	a, _ := s.EnsureUser("app-a")
	if err := s.EnqueuePendingGroupEvent("grp_x", a, PendingGroupEventSync, "G", ""); err != nil {
		t.Fatal(err)
	}
	expectQueued(t, "group event", func() int { l, _ := s.GetPendingGroupEvents(a); return len(l) },
		func(now time.Time) { s.ReapExpiredPendingGroupEvents(now) })
}

func expectQueued(t *testing.T, what string, count func() int, reap func(now time.Time)) {
	t.Helper()
	if pendingQueueMaxAge != 30*24*time.Hour {
		t.Fatalf("pendingQueueMaxAge is %v, want 30 days", pendingQueueMaxAge)
	}
	now := time.Now()
	reap(now.Add(29 * 24 * time.Hour))
	if count() != 1 {
		t.Fatalf("a %s 29 days old was dropped", what)
	}
	reap(now.Add(31 * 24 * time.Hour))
	if count() != 0 {
		t.Fatalf("a %s 31 days old was kept", what)
	}
}
