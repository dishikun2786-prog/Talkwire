package push

import (
	"encoding/base64"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// The data maps below are exactly what the server sent before this package
// existed; Android clients parse them.
func TestDataMatchesLegacyPayloads(t *testing.T) {
	cases := []struct {
		name string
		n    Notification
		want map[string]string
	}{
		{"direct message", Notification{Topic: "uid_a", SenderName: "Ann", SenderAvatar: "https://x/a.png", Body: "hi"},
			map[string]string{"topic": "uid_a", "senderName": "Ann", "preview": "hi", "isGroup": "false", "senderAvatar": "https://x/a.png"}},
		{"direct message, no avatar", Notification{Topic: "uid_a", SenderName: "Ann", Body: "hi"},
			map[string]string{"topic": "uid_a", "senderName": "Ann", "preview": "hi", "isGroup": "false"}},
		{"group message", Notification{Topic: "grp_1", IsGroup: true, SenderName: "Ann", GroupName: "G", Body: "hi"},
			map[string]string{"topic": "grp_1", "senderName": "Ann", "preview": "hi", "isGroup": "true", "groupName": "G"}},
		{"group message, unnamed group", Notification{Topic: "grp_1", IsGroup: true, SenderName: "Ann", Body: "hi"},
			map[string]string{"topic": "grp_1", "senderName": "Ann", "preview": "hi", "isGroup": "true"}},
		{"encrypted", Notification{Kind: KindE2eMessage, Topic: "uid_a"},
			map[string]string{"type": "e2e_message", "e2e": "true", "topic": "uid_a", "isGroup": "false"}},
		{"join notice", Notification{Kind: KindGroupJoin, Topic: "grp_1", IsGroup: true, GroupName: "G", Body: "approved"},
			map[string]string{"type": "group_join", "topic": "grp_1", "groupName": "G", "preview": "approved", "isGroup": "true"}},
		{"join notice, unnamed group", Notification{Kind: KindGroupJoin, Topic: "grp_1", IsGroup: true, Body: "approved"},
			map[string]string{"type": "group_join", "topic": "grp_1", "groupName": "", "preview": "approved", "isGroup": "true"}},
	}
	for _, c := range cases {
		if got := c.n.Data(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got  %v\n want %v", c.name, got, c.want)
		}
	}
}

func TestR12_EncryptedMessageHasNoPreview(t *testing.T) {
	// Even if a caller fills in what it should not, nothing of it leaves.
	n := Notification{Kind: KindE2eMessage, Topic: "uid_a", SenderName: "Ann", SenderAvatar: "https://x/a.png", GroupName: "G", Body: "secret"}
	secrets := []string{"Ann", "a.png", "secret", `"G"`}
	for name, payload := range map[string]func() ([]byte, error){
		"apns":    func() ([]byte, error) { return apnsPayload(n) },
		"webpush": func() ([]byte, error) { return webPushPlaintext(n) },
	} {
		b, err := payload()
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range secrets {
			if strings.Contains(string(b), s) {
				t.Errorf("%s payload leaks %s: %s", name, s, b)
			}
		}
		if !strings.Contains(string(b), LocKeyE2eMessage) {
			t.Errorf("%s payload lacks the localisation key: %s", name, b)
		}
	}
	want := map[string]string{"type": "e2e_message", "e2e": "true", "topic": "uid_a", "isGroup": "false"}
	if got := n.Data(); !reflect.DeepEqual(got, want) {
		t.Errorf("data %v, want only the routing keys %v", got, want)
	}
}

func TestAlert(t *testing.T) {
	for _, c := range []struct {
		n    Notification
		want alert
	}{
		{Notification{SenderName: "Ann", Body: "hi"}, alert{Title: "Ann", Body: "hi"}},
		{Notification{IsGroup: true, GroupName: "G", SenderName: "Ann", Body: "hi"}, alert{Title: "G", Subtitle: "Ann", Body: "hi"}},
		{Notification{IsGroup: true, SenderName: "Ann", Body: "hi"}, alert{Title: "Ann", Body: "hi"}},
		{Notification{Kind: KindGroupJoin, GroupName: "G", Body: "approved"}, alert{Title: "G", Body: "approved"}},
		{Notification{Kind: KindE2eMessage, SenderName: "Ann"}, alert{LocKey: LocKeyE2eMessage}},
	} {
		if got := c.n.alert(); got != c.want {
			t.Errorf("%+v: alert %+v, want %+v", c.n, got, c.want)
		}
	}
}

func TestNormalizeToken(t *testing.T) {
	pub := base64.RawURLEncoding.EncodeToString(mustB64(rfcUAPublic))
	auth := base64.RawURLEncoding.EncodeToString(mustB64(rfcAuthSecret))
	sub := `{"endpoint":"https://fcm.googleapis.com/fcm/send/abc","expirationTime":null,"keys":{"p256dh":"` + pub + `==","auth":"` + auth + `"}}`
	canonical := `{"endpoint":"https://fcm.googleapis.com/fcm/send/abc","keys":{"auth":"` + auth + `","p256dh":"` + pub + `"}}`
	hex := strings.Repeat("Ab", 32)
	ok := []struct{ provider, raw, wantProvider, want string }{
		{"", "legacy token", FCM, "legacy token"},
		{FCM, "x", FCM, "x"},
		{APNs, hex, APNs, strings.ToLower(hex)},
		{WebPush, sub, WebPush, canonical},
		{WebPush, canonical, WebPush, canonical},
	}
	for _, c := range ok {
		p, v, err := NormalizeToken(c.provider, c.raw, DefaultWebPushHosts)
		if err != nil || p != c.wantProvider || v != c.want {
			t.Errorf("NormalizeToken(%q, %.40q) = %q, %q, %v; want %q, %q", c.provider, c.raw, p, v, err, c.wantProvider, c.want)
		}
	}
	bad := []struct{ provider, raw string }{
		{"sms", "x"},
		{APNs, "abc"},
		{APNs, strings.Repeat("zz", 32)},
		{WebPush, "not json"},
		{WebPush, strings.Replace(sub, "https://fcm.googleapis.com", "http://fcm.googleapis.com", 1)},
		{WebPush, strings.Replace(sub, "https://fcm.googleapis.com", "https://169.254.169.254", 1)},
		{WebPush, strings.Replace(sub, pub, "AAAA", 1)},
		{WebPush, strings.Replace(sub, auth, "AAAA", 1)},
	}
	for _, c := range bad {
		if _, _, err := NormalizeToken(c.provider, c.raw, DefaultWebPushHosts); err == nil {
			t.Errorf("NormalizeToken(%q, %.60q) accepted", c.provider, c.raw)
		}
	}
}

func TestHostsAllow(t *testing.T) {
	hosts := Hosts{"push.example.com", "*.notify.windows.com"}
	for raw, want := range map[string]bool{
		"https://push.example.com/x":             true,
		"https://PUSH.example.com:443/x":         true,
		"https://push.example.com:8443/x":        false,
		"http://push.example.com/x":              false,
		"https://user@push.example.com/x":        false,
		"https://evil.push.example.com/x":        false,
		"https://push.example.com.evil.net/x":    false,
		"https://wns2-par02p.notify.windows.com": true,
		"https://notify.windows.com/x":           false,
		"https://127.0.0.1/x":                    false,
	} {
		u, _ := url.Parse(raw)
		if got := hosts.Allow(u); got != want {
			t.Errorf("Allow(%s) = %v, want %v", raw, got, want)
		}
	}
	any := Hosts{"*"}
	u, _ := url.Parse("https://127.0.0.1:9999/x")
	if !any.Allow(u) {
		t.Error(`"*" refused a host`)
	}
	u, _ = url.Parse("http://127.0.0.1/x")
	if any.Allow(u) {
		t.Error(`"*" allowed plain http`)
	}
	if got := ParseHosts(" a.com, ,*.B.com "); !reflect.DeepEqual(got, Hosts{"a.com", "*.b.com"}) {
		t.Errorf("ParseHosts = %v", got)
	}
}

func TestDispatcher(t *testing.T) {
	if NewDispatcher(map[string]Sender{FCM: nil}) != nil {
		t.Error("a dispatcher without senders is not nil")
	}
	rec := &recordingSender{}
	d := NewDispatcher(map[string]Sender{APNs: rec})
	if err := d.Send(t.Context(), Token{Provider: FCM, Value: "x"}, Notification{}); err != ErrNotConfigured {
		t.Errorf("unconfigured provider: %v", err)
	}
	if err := d.Send(t.Context(), Token{Provider: APNs, Value: "y"}, Notification{Body: "b"}); err != nil || rec.token != "y" || rec.n.Body != "b" {
		t.Errorf("send: %v %+v", err, rec)
	}
	if got := d.Providers(); !reflect.DeepEqual(got, []string{APNs}) {
		t.Errorf("Providers = %v", got)
	}
}
