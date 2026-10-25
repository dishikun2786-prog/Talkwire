package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// RFC 8291 section 5 and appendix A (copied from the RFC text, whitespace
// removed).
const (
	rfcPlaintext  = "V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24"
	rfcASPublic   = "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcASPrivate  = "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"
	rfcUAPublic   = "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"
	rfcUAPrivate  = "q1dXpw3UpT5VOmu_cf_v6ih07Aems3njxI-JWgLcM94"
	rfcSalt       = "DGv6ra1nlYgDCS1FRnbzlw"
	rfcAuthSecret = "BTBZMqHH6r4Tts7J_aSIgg"

	rfcECDHSecret = "kyrL1jIIOHEzg3sM2ZWRHDRB62YACZhhSlknJ672kSs"
	rfcPRKKey     = "Snr3JMxaHVDXHWJn5wdC52WjpCtd2EIEGBykDcZW32k"
	rfcIKM        = "S4lYMb_L0FxCeq0WhDx813KgSYqU26kOyzWUdsXYyrg"
	rfcPRK        = "09_eUZGrsvxChDCGRCdkLiDXrReGOEVeSCdCcPBSJSc"
	rfcCEK        = "oIhVW04MRdy2XN9CiKLxTg"
	rfcNonce      = "4h_95klXJ5E_qnoN"
	rfcHeader     = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"
	rfcBody       = "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
)

func mustB64(s string) []byte {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestRFC8291Example(t *testing.T) {
	asPriv, err := ecdh.P256().NewPrivateKey(mustB64(rfcASPrivate))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(asPriv.PublicKey().Bytes(), mustB64(rfcASPublic)) {
		t.Fatal("the as_private of the example does not give its as_public")
	}
	ua, _ := ecdh.P256().NewPublicKey(mustB64(rfcUAPublic))
	secret, _ := asPriv.ECDH(ua)
	k, err := deriveWebPushKeys(secret, mustB64(rfcAuthSecret), mustB64(rfcUAPublic), mustB64(rfcASPublic), mustB64(rfcSalt))
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		got  []byte
		want string
	}{
		"ecdh_secret": {secret, rfcECDHSecret},
		"PRK_key":     {k.prkKey, rfcPRKKey},
		"IKM":         {k.ikm, rfcIKM},
		"PRK":         {k.prk, rfcPRK},
		"CEK":         {k.cek, rfcCEK},
		"NONCE":       {k.nonce, rfcNonce},
	} {
		if got := base64.RawURLEncoding.EncodeToString(c.got); got != c.want {
			t.Errorf("%s = %s, want %s", name, got, c.want)
		}
	}
	body, err := encryptWebPush(mustB64(rfcPlaintext), mustB64(rfcUAPublic), mustB64(rfcAuthSecret), asPriv, mustB64(rfcSalt))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(body, mustB64(rfcHeader)) {
		t.Error("header differs from the RFC's")
	}
	if got := base64.RawURLEncoding.EncodeToString(body); got != rfcBody {
		t.Errorf("body\n got  %s\n want %s", got, rfcBody)
	}
}

// decryptWebPush is the user agent's side, to check what the sender sent.
func decryptWebPush(t *testing.T, body []byte, uaPriv *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	if len(body) < webPushHeaderSize {
		t.Fatal("body shorter than the header")
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != webPushRecordSize || idlen != 65 {
		t.Fatalf("header rs=%d idlen=%d", rs, idlen)
	}
	asPublic := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := uaPriv.ECDH(as)
	k, err := deriveWebPushKeys(secret, authSecret, uaPriv.PublicKey().Bytes(), asPublic, salt)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := aes.NewCipher(k.cek)
	gcm, _ := cipher.NewGCM(block)
	record, err := gcm.Open(nil, k.nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if record[len(record)-1] != 0x02 {
		t.Fatal("record does not end with the last-record delimiter")
	}
	return record[:len(record)-1]
}

func TestRFC8291ExampleDecrypts(t *testing.T) {
	uaPriv, _ := ecdh.P256().NewPrivateKey(mustB64(rfcUAPrivate))
	got := decryptWebPush(t, mustB64(rfcBody), uaPriv, mustB64(rfcAuthSecret))
	if string(got) != "When I grow up, I want to be a watermelon" {
		t.Errorf("plaintext %q", got)
	}
}

func testVAPIDKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// browser is a subscription with its private half.
type browser struct {
	priv  *ecdh.PrivateKey
	auth  []byte
	token string
}

func newBrowser(t *testing.T, endpoint string) browser {
	t.Helper()
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	raw, _ := json.Marshal(map[string]any{"endpoint": endpoint, "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(auth),
	}})
	_, token, err := NormalizeToken(WebPush, string(raw), Hosts{"*"})
	if err != nil {
		t.Fatal(err)
	}
	return browser{priv: priv, auth: auth, token: token}
}

func TestWebPushSend(t *testing.T) {
	vapid := testVAPIDKey(t)
	type received struct {
		header http.Header
		body   []byte
	}
	got := make(chan received, 1)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- received{r.Header.Clone(), b}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	ws, err := newWebPush(vapid, "mailto:ops@example.com", Hosts{"*"}, time.Hour, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	br := newBrowser(t, srv.URL+"/push/abc")
	n := Notification{Topic: "grp_1", IsGroup: true, GroupName: "G", SenderName: "Ann", Body: "hi"}
	if err := ws.Send(context.Background(), br.token, n); err != nil {
		t.Fatal(err)
	}
	r := <-got
	for k, want := range map[string]string{"Content-Encoding": "aes128gcm", "TTL": "3600", "Urgency": "high"} {
		if r.header.Get(k) != want {
			t.Errorf("header %s = %q, want %q", k, r.header.Get(k), want)
		}
	}

	var payload webPushPayload
	if err := json.Unmarshal(decryptWebPush(t, r.body, br.priv, br.auth), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Title != "G - Ann" || payload.Body != "hi" || payload.Data["topic"] != "grp_1" || payload.Data["senderName"] != "Ann" {
		t.Errorf("payload %+v", payload)
	}

	// RFC 8292: "vapid t=<JWT>, k=<public key>", the JWT signed by that key
	// for the push service's origin.
	auth := r.header.Get("Authorization")
	tok, k, ok := strings.Cut(strings.TrimPrefix(auth, "vapid t="), ", k=")
	if !strings.HasPrefix(auth, "vapid t=") || !ok {
		t.Fatalf("Authorization %q", auth)
	}
	if k != ws.PublicKey() {
		t.Error("k is not the VAPID public key")
	}
	claims := jwt.MapClaims{}
	if _, err := jwt.ParseWithClaims(tok, claims, func(*jwt.Token) (any, error) { return &vapid.PublicKey, nil },
		jwt.WithValidMethods([]string{"ES256"})); err != nil {
		t.Fatalf("VAPID token: %v", err)
	}
	exp, _ := claims.GetExpirationTime()
	if claims["aud"] != srv.URL || claims["sub"] != "mailto:ops@example.com" || exp == nil || time.Until(exp.Time) > 24*time.Hour {
		t.Errorf("claims %v", claims)
	}
}

func TestWebPushResponses(t *testing.T) {
	var status atomic.Int32
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/elsewhere" {
			t.Error("a redirect was followed")
		}
		if s := int(status.Load()); s == http.StatusMovedPermanently {
			http.Redirect(w, r, "/elsewhere", s)
			return
		}
		w.WriteHeader(int(status.Load()))
	}))
	defer srv.Close()
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ws, _ := newWebPush(testVAPIDKey(t), "", Hosts{"*"}, 0, client)
	br := newBrowser(t, srv.URL+"/push/abc")
	for code, want := range map[int]error{
		http.StatusCreated:               nil,
		http.StatusGone:                  ErrTokenGone,
		http.StatusNotFound:              ErrTokenGone,
		http.StatusRequestEntityTooLarge: ErrPayloadTooLarge,
	} {
		status.Store(int32(code))
		if err := ws.Send(context.Background(), br.token, Notification{Body: "x"}); !errors.Is(err, want) {
			t.Errorf("HTTP %d: %v, want %v", code, err, want)
		}
	}
	for _, code := range []int{http.StatusTooManyRequests, http.StatusMovedPermanently} {
		status.Store(int32(code))
		if err := ws.Send(context.Background(), br.token, Notification{Body: "x"}); err == nil || errors.Is(err, ErrTokenGone) {
			t.Errorf("HTTP %d: %v, want a plain error", code, err)
		}
	}

	// A narrowed allow-list stops the send before any request.
	before := hits.Load()
	strict, _ := newWebPush(testVAPIDKey(t), "", Hosts{"push.example.com"}, 0, client)
	if err := strict.Send(context.Background(), br.token, Notification{}); err == nil {
		t.Error("send to a host outside the allow-list")
	}
	if hits.Load() != before {
		t.Error("a request reached a host outside the allow-list")
	}
}

func TestWebPushPayloadLimit(t *testing.T) {
	n := Notification{SenderName: "Ann", Body: "hi", SenderAvatar: "https://x/" + strings.Repeat("a", 5000)}
	b, err := webPushPlaintext(n)
	if err != nil || strings.Contains(string(b), "senderAvatar") {
		t.Fatalf("oversized avatar not dropped: %v", err)
	}
	n.SenderAvatar, n.Body = "", strings.Repeat("b", 5000)
	if _, err := webPushPlaintext(n); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("oversized body: %v", err)
	}
}

func TestVAPIDTokenReused(t *testing.T) {
	ws, _ := newWebPush(testVAPIDKey(t), "", Hosts{"*"}, 0, http.DefaultClient)
	now := time.Unix(1_700_000_000, 0)
	ws.now = func() time.Time { return now }
	u := mustURL("https://push.example.com/a")
	first, _ := ws.authorization(u)
	now = now.Add(10 * time.Hour)
	if second, _ := ws.authorization(u); second != first {
		t.Error("token not reused within its lifetime")
	}
	now = now.Add(90 * time.Minute)
	if third, _ := ws.authorization(u); third == first {
		t.Error("token reused within an hour of its expiry")
	}
}

func TestLoadVAPIDKeyFormats(t *testing.T) {
	k := testVAPIDKey(t)
	dir := t.TempDir()
	sec1, _ := x509.MarshalECPrivateKey(k)
	pkcs8, _ := x509.MarshalPKCS8PrivateKey(k)
	for name, block := range map[string]*pem.Block{
		"sec1.pem":  {Type: "EC PRIVATE KEY", Bytes: sec1},
		"pkcs8.pem": {Type: "PRIVATE KEY", Bytes: pkcs8},
	} {
		path := filepath.Join(dir, name)
		os.WriteFile(path, pem.EncodeToMemory(block), 0o600)
		ws, err := NewWebPush(WebPushConfig{KeyPath: path})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		pub, _ := k.PublicKey.ECDH()
		if !bytes.Equal(mustB64(ws.PublicKey()), pub.Bytes()) {
			t.Errorf("%s: wrong public key", name)
		}
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

// recordingSender remembers the last send.
type recordingSender struct {
	token string
	n     Notification
}

func (r *recordingSender) Send(_ context.Context, token string, n Notification) error {
	r.token, r.n = token, n
	return nil
}
