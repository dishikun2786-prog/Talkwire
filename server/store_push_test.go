package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"example.com/imkit/server/push"
	_ "github.com/lib/pq"
)

// testStore opens a migrated core schema in a fresh PostgreSQL schema of the
// database named by IMKIT_TEST_DSN.
func testStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("IMKIT_TEST_DSN")
	if dsn == "" {
		t.Skip("set IMKIT_TEST_DSN to a throwaway PostgreSQL database to run the database tests")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	if _, err := admin.Exec(`DROP SCHEMA IF EXISTS imkit_store_test CASCADE; CREATE SCHEMA imkit_store_test`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec(`DROP SCHEMA IF EXISTS imkit_store_test CASCADE`) })
	s, err := NewStore(dsn + " search_path=imkit_store_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.InitTables(); err != nil {
		t.Fatal(err)
	}
	return s
}

func tokensOf(t *testing.T, s *Store, uids ...string) map[string][]push.Token {
	t.Helper()
	byUID, err := s.GetPushTokensBatch(uids)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string][]push.Token{}
	for uid, ts := range byUID {
		for _, dt := range ts {
			m[uid] = append(m[uid], dt.Token)
		}
		sort.Slice(m[uid], func(i, j int) bool { return m[uid][i].Value < m[uid][j].Value })
	}
	return m
}

func TestPushTokenStore(t *testing.T) {
	s := testStore(t)
	apnsToken := strings.Repeat("ab", 32)
	for _, r := range []struct{ uid, provider, token string }{
		{"uid_1", push.FCM, "t1"},
		{"uid_1", push.APNs, apnsToken},
		{"uid_2", push.WebPush, `{"endpoint":"https://fcm.googleapis.com/x"}`},
	} {
		if err := s.SavePushToken(r.uid, r.provider, r.token, ""); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string][]push.Token{
		"uid_1": {{Provider: push.APNs, Value: apnsToken}, {Provider: push.FCM, Value: "t1"}},
		"uid_2": {{Provider: push.WebPush, Value: `{"endpoint":"https://fcm.googleapis.com/x"}`}},
	}
	if got := tokensOf(t, s, "uid_1", "uid_2", "uid_3"); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokens %v, want %v", got, want)
	}

	// A token moves with the account that registers it last.
	if err := s.SavePushToken("uid_2", push.FCM, "t1", ""); err != nil {
		t.Fatal(err)
	}
	got := tokensOf(t, s, "uid_1", "uid_2")
	if len(got["uid_1"]) != 1 || len(got["uid_2"]) != 2 {
		t.Fatalf("after reassignment: %v", got)
	}
	// The previous owner's logout does not remove it from the new owner.
	s.DeletePushToken("uid_1", "t1")
	if len(tokensOf(t, s, "uid_2")["uid_2"]) != 2 {
		t.Fatal("another account's unreg removed the token")
	}
	if err := s.DeletePushTokenByValue("t1"); err != nil {
		t.Fatal(err)
	}
	if len(tokensOf(t, s, "uid_2")["uid_2"]) != 1 {
		t.Fatal("token reported gone was not removed")
	}
}

func TestPushTokenRowsBeforeProviderColumnAreFCM(t *testing.T) {
	s := testStore(t)
	// What the server wrote before migration core/0002.
	if _, err := s.db.Exec(`INSERT INTO im_fcm_tokens (uid, token) VALUES ('uid_1', 'old')`); err != nil {
		t.Fatal(err)
	}
	if got := tokensOf(t, s, "uid_1")["uid_1"]; !reflect.DeepEqual(got, []push.Token{{Provider: push.FCM, Value: "old"}}) {
		t.Errorf("legacy row read as %v", got)
	}
}

func TestRegTokenByProvider(t *testing.T) {
	s := testStore(t)
	h := &Hub{store: s, webPushHosts: push.DefaultWebPushHosts}
	c := &Client{hub: h, uid: "uid_1", send: make(chan []byte, 4)}

	key := make([]byte, 65)
	key[0] = 4
	// A valid P-256 point: the generator.
	gx, _ := base64.RawURLEncoding.DecodeString("axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpY")
	gy, _ := base64.RawURLEncoding.DecodeString("T-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU")
	copy(key[1:], gx)
	copy(key[33:], gy)
	sub, _ := json.Marshal(map[string]any{
		"endpoint":       "https://updates.push.services.mozilla.com/wpush/v2/abc",
		"expirationTime": nil,
		"keys": map[string]string{
			"p256dh": base64.URLEncoding.EncodeToString(key), // padded, as some browsers send it
			"auth":   base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
		},
	})

	h.handleRegToken(c, &ClientMessage{Action: ActionRegToken, Token: "fcm-legacy"})
	h.handleRegToken(c, &ClientMessage{Action: ActionRegToken, Provider: push.WebPush, Token: string(sub)})
	h.handleRegToken(c, &ClientMessage{Action: ActionRegToken, Provider: push.APNs, Token: "not hex"})
	h.handleRegToken(c, &ClientMessage{Action: ActionRegToken, Provider: "sms", Token: "x"})
	h.handleRegToken(c, &ClientMessage{Action: ActionRegToken, Provider: push.WebPush,
		Token: strings.Replace(string(sub), "updates.push.services.mozilla.com", "evil.example", 1)})

	got := byProvider(tokensOf(t, s, "uid_1")["uid_1"])
	if len(got) != 2 || got[push.FCM] != "fcm-legacy" || got[push.WebPush] == "" {
		t.Fatalf("stored %v, want the FCM token and the web push subscription only", got)
	}
	if v := got[push.WebPush]; strings.Contains(v, "expirationTime") || strings.Contains(v, "=") {
		t.Errorf("subscription not stored in canonical form: %s", v)
	}
	if len(c.send) != 0 {
		t.Error("reg_token replied; it is fire-and-forget")
	}

	// Unregistering with the browser's own (non-canonical) JSON finds the row.
	h.handleUnregToken(c, &ClientMessage{Action: ActionUnregToken, Provider: push.WebPush, Token: string(sub)})
	if got := byProvider(tokensOf(t, s, "uid_1")["uid_1"]); len(got) != 1 || got[push.FCM] == "" {
		t.Errorf("after unreg: %v", got)
	}
}

func byProvider(ts []push.Token) map[string]string {
	m := map[string]string{}
	for _, t := range ts {
		m[t.Provider] = t.Value
	}
	return m
}

type fakeSender struct {
	err  error
	sent []string
}

func (f *fakeSender) Send(_ context.Context, token string, _ push.Notification) error {
	f.sent = append(f.sent, token)
	return f.err
}

func TestSendPushRoutesAndPrunes(t *testing.T) {
	s := testStore(t)
	apnsToken := strings.Repeat("cd", 32)
	s.SavePushToken("uid_1", push.FCM, "f1", "")
	s.SavePushToken("uid_1", push.APNs, apnsToken, "")
	s.SavePushToken("uid_1", push.WebPush, "w1", "")
	fcm, apns := &fakeSender{}, &fakeSender{err: push.ErrTokenGone}
	h := &Hub{store: s, pusher: push.NewDispatcher(map[string]push.Sender{push.FCM: fcm, push.APNs: apns})}

	h.sendPush("uid_1", tokensOf(t, s, "uid_1")["uid_1"], push.Notification{Body: "hi"}, "test")

	if !reflect.DeepEqual(fcm.sent, []string{"f1"}) || !reflect.DeepEqual(apns.sent, []string{apnsToken}) {
		t.Errorf("routed fcm=%v apns=%v", fcm.sent, apns.sent)
	}
	got := byProvider(tokensOf(t, s, "uid_1")["uid_1"])
	if _, ok := got[push.APNs]; ok {
		t.Error("a token reported gone was kept")
	}
	if got[push.FCM] != "f1" || got[push.WebPush] != "w1" {
		t.Errorf("tokens left %v; the unconfigured provider's token must stay", got)
	}
}
