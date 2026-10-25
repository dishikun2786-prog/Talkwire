package hmacjwt

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const secret = "test-secret-0123456789"

func sign(t *testing.T, key string, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestUserID(t *testing.T) {
	v := New(secret)
	exp := time.Now().Add(time.Hour).Unix()
	got, err := v.UserID(sign(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"sub": "alice", "type": "access", "exp": exp}))
	if err != nil || got != "alice" {
		t.Fatalf("valid token: got %q, %v", got, err)
	}
	for name, tok := range map[string]string{
		"empty":        "",
		"wrong secret": sign(t, "other-secret", jwt.SigningMethodHS256, jwt.MapClaims{"sub": "alice", "exp": exp}),
		"expired":      sign(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"sub": "alice", "exp": time.Now().Add(-time.Minute).Unix()}),
		"no sub":       sign(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"exp": exp}),
		"garbage":      "not.a.token",
	} {
		if _, err := v.UserID(tok); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestUserIDRejectsNonHMAC(t *testing.T) {
	// "alg":"none" must never pass, whatever the claims say.
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "alice"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(secret).UserID(tok); err == nil {
		t.Fatal("alg none accepted")
	}
}

func TestCheckInternalToken(t *testing.T) {
	v := New(secret)
	exp := time.Now().Add(time.Minute).Unix()
	if err := v.CheckInternalToken(sign(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"role": "internal_service", "exp": exp})); err != nil {
		t.Fatalf("internal token rejected: %v", err)
	}
	// An end-user token, even a valid one, is not an internal token.
	err := v.CheckInternalToken(sign(t, secret, jwt.SigningMethodHS256, jwt.MapClaims{"sub": "alice", "exp": exp}))
	if err == nil || !strings.Contains(err.Error(), "internal service role required") {
		t.Fatalf("user token accepted as internal: %v", err)
	}
	if v.CheckInternalToken("") == nil {
		t.Fatal("empty token accepted")
	}
}
