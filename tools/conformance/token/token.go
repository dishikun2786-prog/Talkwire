// Package token mints the HS256 tokens the IM server accepts, for tests only.
//
// The real tokens are issued by the host (the business backend). The server
// verifies them with a shared secret, so a test harness that knows the secret
// can impersonate any user or the host itself.
package token

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Sign signs arbitrary claims with HS256.
func Sign(secret string, claims map[string]any) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims)).SignedString([]byte(secret))
}

// User returns an access token for appUserID. Entries in overrides replace the
// defaults; an override whose value is nil removes that claim.
func User(secret, appUserID string, now time.Time, overrides map[string]any) (string, error) {
	claims := map[string]any{
		"sub":  appUserID,
		"role": "app_user",
		"type": "access",
		"iat":  now.Unix(),
		"exp":  now.Add(10 * time.Minute).Unix(),
	}
	for k, v := range overrides {
		if v == nil {
			delete(claims, k)
			continue
		}
		claims[k] = v
	}
	return Sign(secret, claims)
}

// Internal returns a service-to-service token in the shape the IM server's
// ValidateInternalToken expects (role=internal_service, no subject).
func Internal(secret string, now time.Time) (string, error) {
	return Sign(secret, map[string]any{
		"role": "internal_service",
		"exp":  now.Add(time.Minute).Unix(),
	})
}

// Verify parses an HS256 token and returns its claims.
func Verify(secret, raw string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	return claims, err
}
