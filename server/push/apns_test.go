package push

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testDevice = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newAPNsServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestAPNsSend(t *testing.T) {
	key := testVAPIDKey(t) // any P-256 key
	type received struct {
		proto  string
		path   string
		header http.Header
		body   map[string]any
	}
	got := make(chan received, 1)
	srv := newAPNsServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		got <- received{r.Proto, r.URL.Path, r.Header.Clone(), body}
	})
	a := newAPNs(key, "KEYID12345", "TEAMID1234", "com.example.app", srv.URL, srv.Client())
	n := Notification{Topic: "grp_1", IsGroup: true, GroupName: "G", SenderName: "Ann", Body: "hi"}
	if err := a.Send(context.Background(), testDevice, n); err != nil {
		t.Fatal(err)
	}
	r := <-got
	if r.proto != "HTTP/2.0" {
		t.Errorf("protocol %s, APNs requires HTTP/2", r.proto)
	}
	if r.path != "/3/device/"+testDevice {
		t.Errorf("path %s", r.path)
	}
	for k, want := range map[string]string{"Apns-Topic": "com.example.app", "Apns-Push-Type": "alert", "Apns-Priority": "10"} {
		if r.header.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, r.header.Get(k), want)
		}
	}
	if exp, _ := strconv.ParseInt(r.header.Get("Apns-Expiration"), 10, 64); time.Until(time.Unix(exp, 0)) < 23*time.Hour {
		t.Errorf("apns-expiration %q", r.header.Get("Apns-Expiration"))
	}
	bearer := strings.TrimPrefix(r.header.Get("Authorization"), "bearer ")
	claims := jwt.MapClaims{}
	tok, err := jwt.ParseWithClaims(bearer, claims, func(*jwt.Token) (any, error) { return &key.PublicKey, nil },
		jwt.WithValidMethods([]string{"ES256"}))
	if err != nil || tok.Header["kid"] != "KEYID12345" || claims["iss"] != "TEAMID1234" {
		t.Errorf("provider token: %v %v %v", err, tok.Header, claims)
	}
	aps, _ := r.body["aps"].(map[string]any)
	alert, _ := aps["alert"].(map[string]any)
	if alert["title"] != "G" || alert["subtitle"] != "Ann" || alert["body"] != "hi" || aps["thread-id"] != "grp_1" || aps["mutable-content"] != float64(1) {
		t.Errorf("aps %v", aps)
	}
	if r.body["topic"] != "grp_1" || r.body["senderName"] != "Ann" || r.body["groupName"] != "G" {
		t.Errorf("custom keys %v", r.body)
	}
}

func TestAPNsEncryptedAlert(t *testing.T) {
	b, err := apnsPayload(Notification{Kind: KindE2eMessage, Topic: "uid_a"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"aps":{"alert":{"loc-key":"IMKIT_E2E_MESSAGE"},"sound":"default","thread-id":"uid_a","mutable-content":1},"e2e":"true","isGroup":"false","topic":"uid_a","type":"e2e_message"}`
	if string(b) != want {
		t.Errorf("payload\n got  %s\n want %s", b, want)
	}
}

func TestAPNsResponses(t *testing.T) {
	var status atomic.Int32
	var reason atomic.Value
	srv := newAPNsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(status.Load()))
		io.WriteString(w, `{"reason":"`+reason.Load().(string)+`"}`)
	})
	a := newAPNs(testVAPIDKey(t), "K", "T", "com.example.app", srv.URL, srv.Client())
	for _, c := range []struct {
		status int
		reason string
		want   error
	}{
		{http.StatusOK, "", nil},
		{http.StatusGone, "Unregistered", ErrTokenGone},
		{http.StatusBadRequest, "BadDeviceToken", ErrTokenGone},
		{http.StatusRequestEntityTooLarge, "PayloadTooLarge", ErrPayloadTooLarge},
	} {
		status.Store(int32(c.status))
		reason.Store(c.reason)
		if err := a.Send(context.Background(), testDevice, Notification{}); !errors.Is(err, c.want) {
			t.Errorf("%d %s: %v, want %v", c.status, c.reason, err, c.want)
		}
	}
	for _, c := range []struct {
		status int
		reason string
	}{{http.StatusBadRequest, "DeviceTokenNotForTopic"}, {http.StatusTooManyRequests, "TooManyRequests"}, {http.StatusServiceUnavailable, "ServiceUnavailable"}} {
		status.Store(int32(c.status))
		reason.Store(c.reason)
		if err := a.Send(context.Background(), testDevice, Notification{}); err == nil || errors.Is(err, ErrTokenGone) {
			t.Errorf("%d %s: %v, want a plain error", c.status, c.reason, err)
		}
	}

	// A rejected provider token is not reused.
	if _, err := a.providerToken(); err != nil {
		t.Fatal(err)
	}
	status.Store(http.StatusForbidden)
	reason.Store("ExpiredProviderToken")
	a.Send(context.Background(), testDevice, Notification{})
	a.mu.Lock()
	kept := a.jwt
	a.mu.Unlock()
	if kept != "" {
		t.Error("an expired provider token is still cached")
	}
}

func TestAPNsProviderTokenAge(t *testing.T) {
	a := newAPNs(testVAPIDKey(t), "K", "T", "app", "", http.DefaultClient)
	now := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return now }
	first, _ := a.providerToken()
	now = now.Add(39 * time.Minute)
	if again, _ := a.providerToken(); again != first {
		t.Error("token re-minted within 40 minutes")
	}
	now = now.Add(2 * time.Minute)
	if later, _ := a.providerToken(); later == first {
		t.Error("token reused after 40 minutes (Apple rejects tokens older than an hour)")
	}
}

func TestNewAPNsLoadsP8(t *testing.T) {
	der, _ := x509.MarshalPKCS8PrivateKey(testVAPIDKey(t))
	path := filepath.Join(t.TempDir(), "AuthKey_K.p8")
	os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	a, err := NewAPNs(APNsConfig{KeyPath: path, KeyID: "K", TeamID: "T", Topic: "app"})
	if err != nil {
		t.Fatal(err)
	}
	if a.base != APNsProduction {
		t.Errorf("default endpoint %s", a.base)
	}
	if _, err := NewAPNs(APNsConfig{KeyPath: path, KeyID: "K", TeamID: "T"}); err == nil {
		t.Error("accepted a configuration without a topic")
	}
}
