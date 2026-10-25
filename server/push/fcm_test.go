package push

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testFCM(t *testing.T, srv *httptest.Server) (*FCMSender, *rsa.PrivateKey) {
	t.Helper()
	rk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(rk)
	key := &ServiceAccountKey{
		ProjectID: "proj", PrivateKeyID: "kid1", ClientEmail: "svc@proj.iam.gserviceaccount.com",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
	}
	f, err := NewFCM(key, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f.client = srv.Client()
	f.tokenURL = srv.URL + "/token"
	f.sendURL = srv.URL + "/v1/projects/proj/messages:send"
	return f, rk
}

func TestFCMSend(t *testing.T) {
	var tokenCalls atomic.Int32
	var mu sync.Mutex
	var bodies [][]byte
	var rk *rsa.PrivateKey
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenCalls.Add(1)
			r.ParseForm()
			if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Errorf("grant_type %q", r.Form.Get("grant_type"))
			}
			claims := jwt.MapClaims{}
			tok, err := jwt.ParseWithClaims(r.Form.Get("assertion"), claims, func(*jwt.Token) (any, error) { return &rk.PublicKey, nil },
				jwt.WithValidMethods([]string{"RS256"}))
			if err != nil || tok.Header["kid"] != "kid1" || claims["iss"] != "svc@proj.iam.gserviceaccount.com" ||
				claims["scope"] != "https://www.googleapis.com/auth/firebase.messaging" {
				t.Errorf("assertion: %v %v %v", err, tok.Header, claims)
			}
			io.WriteString(w, `{"access_token":"at-1","expires_in":3600}`)
		case "/v1/projects/proj/messages:send":
			if r.Header.Get("Authorization") != "Bearer at-1" {
				t.Errorf("Authorization %q", r.Header.Get("Authorization"))
			}
			b, _ := io.ReadAll(r.Body)
			mu.Lock()
			bodies = append(bodies, b)
			mu.Unlock()
			io.WriteString(w, `{"name":"projects/proj/messages/1"}`)
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	f, key := testFCM(t, srv)
	rk = key
	n := Notification{Topic: "uid_a", SenderName: "Ann", Body: "hi"}
	for range 3 {
		if err := f.Send(context.Background(), "dev-1", n); err != nil {
			t.Fatal(err)
		}
	}
	if tokenCalls.Load() != 1 {
		t.Errorf("token exchanged %d times, want once", tokenCalls.Load())
	}
	// Exactly the message the server sent before this package: data only,
	// high priority on Android.
	want := `{"message":{"token":"dev-1","data":{"isGroup":"false","preview":"hi","senderName":"Ann","topic":"uid_a"},"android":{"priority":"high"}}}`
	if string(bodies[0]) != want {
		t.Errorf("body\n got  %s\n want %s", bodies[0], want)
	}
}

func TestFCMErrors(t *testing.T) {
	var reply atomic.Value
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			io.WriteString(w, `{"access_token":"at-1"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, reply.Load().(string))
	}))
	defer srv.Close()
	f, _ := testFCM(t, srv)
	unregistered := `{"error":{"code":404,"status":"NOT_FOUND","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`
	for body, gone := range map[string]bool{
		unregistered: true,
		`{"error":{"code":404,"details":[{"errorCode":"SENDER_ID_MISMATCH"}]}}`: false,
		`not json`: false,
	} {
		reply.Store(body)
		err := f.Send(context.Background(), "dev-1", Notification{})
		if err == nil || errors.Is(err, ErrTokenGone) != gone {
			t.Errorf("reply %.40s: %v, gone=%v", body, err, gone)
		}
	}
}

func TestFCMTokenExchangeFailureReleasesWaiters(t *testing.T) {
	// F-40: a failed exchange must wake every goroutine waiting for it.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"error":"backend"}`)
	}))
	defer srv.Close()
	f, _ := testFCM(t, srv)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Go(func() { errs <- f.Send(context.Background(), "dev", Notification{}) })
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("senders still waiting after the exchange failed")
	}
	close(errs)
	for err := range errs {
		if err == nil {
			t.Error("send succeeded without an access token")
		}
	}
}

func TestNewFCMRejectsBadKeys(t *testing.T) {
	for name, k := range map[string]ServiceAccountKey{
		"no project":  {ClientEmail: "a@b", PrivateKey: "x"},
		"not PEM":     {ProjectID: "p", ClientEmail: "a@b", PrivateKey: "x"},
		"not PKCS #8": {ProjectID: "p", ClientEmail: "a@b", PrivateKey: "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"},
	} {
		if _, err := NewFCM(&k, time.Second); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestServiceAccountKeyFields(t *testing.T) {
	// The fields NewFCM needs are read from the JSON Google issues.
	var k ServiceAccountKey
	json.Unmarshal([]byte(`{"type":"service_account","project_id":"p","private_key_id":"i","private_key":"k","client_email":"e","client_id":"c"}`), &k)
	if k.ProjectID != "p" || k.PrivateKeyID != "i" || k.PrivateKey != "k" || k.ClientEmail != "e" {
		t.Errorf("%+v", k)
	}
}
