package jwks

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dishikun2786-prog/Talkwire/server/host/hmacjwt"
	"github.com/golang-jwt/jwt/v5"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func rsaJWK(kid string, k *rsa.PrivateKey) map[string]string {
	return map[string]string{"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}
}

func ecJWK(kid string, k *ecdsa.PrivateKey) map[string]string {
	pub, _ := k.PublicKey.Bytes()
	return map[string]string{"kty": "EC", "kid": kid, "crv": "P-256", "x": b64(pub[1:33]), "y": b64(pub[33:])}
}

// keyServer serves a key set that the test can replace.
type keyServer struct {
	srv   *httptest.Server
	set   atomic.Value // []map[string]string
	reads atomic.Int32
}

func newKeyServer(t *testing.T, keys ...map[string]string) *keyServer {
	ks := &keyServer{}
	ks.set.Store(keys)
	ks.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ks.reads.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"keys": ks.set.Load()})
	}))
	t.Cleanup(ks.srv.Close)
	return ks
}

func (ks *keyServer) verifier(t *testing.T, p hmacjwt.Policy) *Verifier {
	v, err := New(ks.srv.URL, p, hmacjwt.New("s"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	v.client = ks.srv.Client()
	return v
}

func sign(t *testing.T, method jwt.SigningMethod, kid string, key any, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	if kid != "" {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func userClaims() jwt.MapClaims {
	return jwt.MapClaims{"sub": "alice", "type": "access", "role": "app_user", "exp": time.Now().Add(time.Hour).Unix()}
}

func TestVerifiesRSAAndEC(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ek, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ks := newKeyServer(t, rsaJWK("r1", rk), ecJWK("e1", ek))
	v := ks.verifier(t, hmacjwt.Policy{AccessOnly: true})
	for name, tok := range map[string]string{
		"RS256": sign(t, jwt.SigningMethodRS256, "r1", rk, userClaims()),
		"ES256": sign(t, jwt.SigningMethodES256, "e1", ek, userClaims()),
	} {
		if got, err := v.UserID(tok); err != nil || got != "alice" {
			t.Errorf("%s: %q, %v", name, got, err)
		}
	}
	if ks.reads.Load() != 1 {
		t.Errorf("key set read %d times, want once", ks.reads.Load())
	}
}

func TestRejects(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	ks := newKeyServer(t, rsaJWK("r1", rk))
	v := ks.verifier(t, hmacjwt.Policy{AccessOnly: true})
	expired := userClaims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()
	refresh := userClaims()
	refresh["type"] = "refresh"
	pub, _ := json.Marshal(rsaJWK("r1", rk))
	for name, tok := range map[string]string{
		"wrong key":     sign(t, jwt.SigningMethodRS256, "r1", other, userClaims()),
		"no kid":        sign(t, jwt.SigningMethodRS256, "", rk, userClaims()),
		"expired":       sign(t, jwt.SigningMethodRS256, "r1", rk, expired),
		"refresh token": sign(t, jwt.SigningMethodRS256, "r1", rk, refresh),
		// Algorithm confusion: HMAC keyed with the public key's bytes.
		"HS256 with the public key": sign(t, jwt.SigningMethodHS256, "r1", pub, userClaims()),
	} {
		if _, err := v.UserID(tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Without the policy the refresh token logs in, like the reference server.
	if _, err := ks.verifier(t, hmacjwt.Policy{}).UserID(sign(t, jwt.SigningMethodRS256, "r1", rk, refresh)); err != nil {
		t.Errorf("no policy: %v", err)
	}
}

func TestKeyRotation(t *testing.T) {
	old, _ := rsa.GenerateKey(rand.Reader, 2048)
	next, _ := rsa.GenerateKey(rand.Reader, 2048)
	ks := newKeyServer(t, rsaJWK("k1", old))
	v := ks.verifier(t, hmacjwt.Policy{})
	now := time.Unix(1_700_000_000, 0)
	v.now = func() time.Time { return now }
	if _, err := v.UserID(sign(t, jwt.SigningMethodRS256, "k1", old, userClaims())); err != nil {
		t.Fatal(err)
	}
	// The host rotates. A token with the new key id is accepted once the
	// re-read is allowed again.
	ks.set.Store([]map[string]string{rsaJWK("k2", next)})
	tok := sign(t, jwt.SigningMethodRS256, "k2", next, userClaims())
	now = now.Add(minInterval + time.Second)
	if _, err := v.UserID(tok); err != nil {
		t.Fatalf("rotated key: %v", err)
	}
}

func TestUnknownKeyIDsAreRateLimited(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ks := newKeyServer(t, rsaJWK("k1", rk))
	v := ks.verifier(t, hmacjwt.Policy{})
	for i := range 20 {
		tok := sign(t, jwt.SigningMethodRS256, "made-up-"+string(rune('a'+i)), rk, userClaims())
		if _, err := v.UserID(tok); err == nil {
			t.Fatal("unknown key id accepted")
		}
	}
	if n := ks.reads.Load(); n != 1 {
		t.Errorf("key set read %d times for 20 unknown key ids, want 1", n)
	}
}

func TestUnusableKeysAreSkipped(t *testing.T) {
	small, _ := rsa.GenerateKey(rand.Reader, 1024)
	good, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	enc := ecJWK("enc", good)
	enc["use"] = "enc"
	ks := newKeyServer(t, rsaJWK("small", small), enc, ecJWK("good", good))
	v := ks.verifier(t, hmacjwt.Policy{})
	if _, err := v.UserID(sign(t, jwt.SigningMethodES256, "good", good, userClaims())); err != nil {
		t.Errorf("usable key refused: %v", err)
	}
	for _, kid := range []string{"small", "enc"} {
		if _, ok := v.keys[kid]; ok {
			t.Errorf("key %s should have been skipped", kid)
		}
	}
	if _, err := v.UserID(sign(t, jwt.SigningMethodRS256, "small", small, userClaims())); err == nil {
		t.Error("a 1024-bit RSA key was accepted")
	}
}

func TestNewRequiresHTTPS(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://host.example/.well-known/jwks.json": true,
		"http://127.0.0.1:6001/jwks":                 true,
		"http://localhost/jwks":                      true,
		"http://host.example/jwks":                   false,
		"not a url":                                  false,
	} {
		if _, err := New(raw, hmacjwt.Policy{}, hmacjwt.New("s"), time.Second); (err == nil) != ok {
			t.Errorf("New(%q): %v", raw, err)
		}
	}
}

func TestInternalTokensUseTheSharedSecret(t *testing.T) {
	v, _ := New("https://host.example/jwks", hmacjwt.Policy{}, hmacjwt.New("s"), time.Second)
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"role": "internal_service", "exp": time.Now().Add(time.Minute).Unix()}).SignedString([]byte("s"))
	if err := v.CheckInternalToken(tok); err != nil {
		t.Errorf("internal token: %v", err)
	}
	if err := v.CheckInternalToken("x"); err == nil {
		t.Error("garbage internal token accepted")
	}
}

func TestOldKeySetRefreshedInBackground(t *testing.T) {
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	ks := newKeyServer(t, rsaJWK("k1", rk))
	v := ks.verifier(t, hmacjwt.Policy{})
	now := time.Unix(1_700_000_000, 0)
	v.now = func() time.Time { return now }
	tok := sign(t, jwt.SigningMethodRS256, "k1", rk, userClaims())
	if _, err := v.UserID(tok); err != nil {
		t.Fatal(err)
	}
	now = now.Add(maxAge + time.Minute)
	if _, err := v.UserID(tok); err != nil {
		t.Fatalf("cached key refused while the set is old: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for ks.reads.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if ks.reads.Load() != 2 {
		t.Errorf("key set read %d times, want a background re-read", ks.reads.Load())
	}
}
