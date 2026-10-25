// Package hmacjwt verifies tokens signed with a secret shared with the host
// (HS256), the identity scheme of the reference deployment.
package hmacjwt

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// Claims represents the JWT claims expected from the auth server
type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

// Verifier validates tokens with one shared secret. It implements
// host.Identity.
type Verifier struct {
	secret []byte
}

// New returns a verifier for the shared secret.
func New(secret string) *Verifier {
	return &Verifier{secret: []byte(secret)}
}

// UserID validates a JWT token and returns the user ID (sub)
func (a *Verifier) UserID(tokenStr string) (string, error) {
	if tokenStr == "" {
		return "", fmt.Errorf("empty token")
	}
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{},
		func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			return a.secret, nil
		},
	)
	if err != nil {
		return "", fmt.Errorf("token validation failed: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}
	if claims.Sub == "" {
		return "", fmt.Errorf("token missing sub (user ID)")
	}
	return claims.Sub, nil
}

// CheckInternalToken validates the short-lived service-to-service token
// minted by the main API. It deliberately accepts no end-user token: the IM
// admin HTTP endpoints are a privileged bridge used only by the main API
// after that API has authenticated an administrator.
func (a *Verifier) CheckInternalToken(tokenStr string) error {
	if tokenStr == "" {
		return fmt.Errorf("empty internal token")
	}
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return a.secret, nil
	})
	if err != nil {
		return fmt.Errorf("internal token validation failed: %w", err)
	}
	if !token.Valid {
		return fmt.Errorf("invalid internal token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || claims["role"] != "internal_service" {
		return fmt.Errorf("internal service role required")
	}
	return nil
}
