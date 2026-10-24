package main

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// IMUserID generates a deterministic user ID from JWT subject
// Format: uid_<11 base64url chars> (similar to Tinode's usrXXX style)
func IMUserID(appUserID string) string {
	h := sha256.Sum256([]byte(appUserID))
	bare := base64.RawURLEncoding.EncodeToString(h[:8])
	return "uid_" + bare
}

// Claims represents the JWT claims expected from the auth server
type Claims struct {
	Sub  string `json:"sub"`
	Role string `json:"role,omitempty"`
	jwt.RegisteredClaims
}

// AuthService validates JWT tokens
type AuthService struct {
	secret []byte
}

func NewAuthService(jwtSecret string) *AuthService {
	return &AuthService{secret: []byte(jwtSecret)}
}

// ValidateToken validates a JWT token and returns the user ID (sub)
func (a *AuthService) ValidateToken(tokenStr string) (string, error) {
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

// GetIMUserID validates the token and returns the IM user ID
func (a *AuthService) GetIMUserID(tokenStr string) (string, error) {
	appUserID, err := a.ValidateToken(tokenStr)
	if err != nil {
		return "", err
	}
	return IMUserID(appUserID), nil
}

// GetAppUserID validates the token and returns the original app user ID (UUID)
func (a *AuthService) GetAppUserID(tokenStr string) (string, error) {
	return a.ValidateToken(tokenStr)
}

// ValidateInternalToken validates the short-lived service-to-service token
// minted by the main API. It deliberately accepts no end-user token: the IM
// admin HTTP endpoints are a privileged bridge used only by the main API
// after that API has authenticated an administrator.
func (a *AuthService) ValidateInternalToken(tokenStr string) error {
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
