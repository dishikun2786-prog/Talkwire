// Package hmacjwt verifies tokens signed with a secret shared with the host
// (HS256), the identity scheme of the reference deployment. Its Claims and
// Policy are also what the JWKS verifier (package jwks) checks.
package hmacjwt

import (
	"errors"
	"fmt"
	"slices"

	"github.com/golang-jwt/jwt/v5"
)

// Claims are the end-user token claims the host issues.
type Claims struct {
	Sub string `json:"sub"`
	// Type is "access" for the token a client should log in with; the host
	// also issues refresh tokens and other kinds.
	Type string `json:"type,omitempty"`
	Role string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

// DefaultUserRoles are the roles of ordinary users in the reference
// deployment.
var DefaultUserRoles = []string{"app_user"}

// Policy is which valid tokens may log in.
type Policy struct {
	// AccessOnly accepts only access tokens of an ordinary user role (red
	// line R18). Without it any valid token with a subject logs in, refresh
	// and administrator tokens included -- the reference server's behaviour.
	AccessOnly bool
	// UserRoles are the ordinary user roles; empty means DefaultUserRoles.
	UserRoles []string
}

// ErrNotUserAccessToken rejects a valid token that is not an end user's
// access token.
var ErrNotUserAccessToken = errors.New("not a user access token")

// User applies the policy to verified claims and returns the host user id.
func (p Policy) User(c *Claims) (string, error) {
	if c.Sub == "" {
		return "", fmt.Errorf("token missing sub (user ID)")
	}
	if p.AccessOnly {
		roles := p.UserRoles
		if len(roles) == 0 {
			roles = DefaultUserRoles
		}
		if c.Type != "access" || !slices.Contains(roles, c.Role) {
			return "", ErrNotUserAccessToken
		}
	}
	return c.Sub, nil
}

// Verifier validates tokens with one shared secret. It implements
// host.Identity.
type Verifier struct {
	secret []byte
	policy Policy
}

// New returns a verifier for the shared secret that accepts any valid user
// token, like the reference server.
func New(secret string) *Verifier {
	return &Verifier{secret: []byte(secret)}
}

// WithPolicy returns a copy of the verifier that applies p to user tokens.
func (a *Verifier) WithPolicy(p Policy) *Verifier {
	c := *a
	c.policy = p
	return &c
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
	return a.policy.User(claims)
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
