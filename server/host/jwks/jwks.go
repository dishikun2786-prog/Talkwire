// Package jwks verifies end-user tokens the host signs with its private key
// (RS256 or ES256), reading the public keys from a JSON Web Key Set
// (RFC 7517) the host publishes. Only the host's private key can then issue
// login tokens; the IM server holds nothing that can.
//
// Internal tokens (the host calling the server's internal endpoints) are
// still checked with the shared secret: the server needs it anyway to call
// the host.
package jwks

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"example.com/imkit/server/host/hmacjwt"
	"github.com/golang-jwt/jwt/v5"
)

// Refresh timing.
const (
	// maxAge is how old the key set may get before it is re-read in the
	// background.
	maxAge = 10 * time.Minute
	// minInterval limits re-reads for unknown key ids, so tokens with made-up
	// key ids cannot make the server hammer the host.
	minInterval = 30 * time.Second
)

// InternalChecker validates the host's internal tokens.
type InternalChecker interface {
	CheckInternalToken(token string) error
}

// Verifier implements host.Identity.
type Verifier struct {
	url      string
	client   *http.Client
	policy   hmacjwt.Policy
	internal InternalChecker
	now      func() time.Time

	mu      sync.RWMutex
	keys    map[string]any // kid -> *rsa.PublicKey or *ecdsa.PublicKey
	fetched time.Time

	// fetchMu serialises reads of the key set; lastTry is guarded by it.
	fetchMu sync.Mutex
	lastTry time.Time
}

// New returns a verifier for the key set at rawURL, which must be https
// (http only on a loopback address). Keys are read on first use.
func New(rawURL string, policy hmacjwt.Policy, internal InternalChecker, timeout time.Duration) (*Verifier, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("jwks: invalid URL %q", rawURL)
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil, fmt.Errorf("jwks: the key set URL must be https")
	}
	return &Verifier{
		url: rawURL, policy: policy, internal: internal, now: time.Now,
		client: &http.Client{Timeout: timeout}, keys: map[string]any{},
	}, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// UserID implements host.Identity.
func (v *Verifier) UserID(tokenStr string) (string, error) {
	if tokenStr == "" {
		return "", errors.New("empty token")
	}
	token, err := jwt.ParseWithClaims(tokenStr, &hmacjwt.Claims{}, v.keyFor,
		jwt.WithValidMethods([]string{"RS256", "ES256"}))
	if err != nil {
		return "", fmt.Errorf("token validation failed: %w", err)
	}
	claims, ok := token.Claims.(*hmacjwt.Claims)
	if !ok || !token.Valid {
		return "", errors.New("invalid token claims")
	}
	return v.policy.User(claims)
}

// CheckInternalToken implements host.Identity.
func (v *Verifier) CheckInternalToken(tokenStr string) error {
	return v.internal.CheckInternalToken(tokenStr)
}

func (v *Verifier) keyFor(t *jwt.Token) (any, error) {
	kid, _ := t.Header["kid"].(string)
	if kid == "" {
		return nil, errors.New("token has no key id")
	}
	v.mu.RLock()
	key, fetched := v.keys[kid], v.fetched
	v.mu.RUnlock()
	if key != nil {
		if v.now().Sub(fetched) > maxAge {
			v.refreshInBackground()
		}
		return key, nil
	}
	// A key the server has not seen: the host may have rotated keys.
	if err := v.refresh(false); err != nil {
		return nil, err
	}
	v.mu.RLock()
	key = v.keys[kid]
	v.mu.RUnlock()
	if key == nil {
		return nil, fmt.Errorf("unknown key id %q", kid)
	}
	return key, nil
}

// Refresh re-reads the key set now.
func (v *Verifier) Refresh() error { return v.refresh(true) }

// refreshInBackground re-reads an old key set without holding up the login
// that noticed: the cached keys stay in use meanwhile, and if a read is
// already in flight nothing more is started.
func (v *Verifier) refreshInBackground() {
	if !v.fetchMu.TryLock() {
		return
	}
	go func() {
		defer v.fetchMu.Unlock()
		if err := v.refreshLocked(false); err != nil {
			log.Printf("[jwks] refreshing the key set failed, keeping the cached keys: %v", err)
		}
	}()
}

func (v *Verifier) refresh(force bool) error {
	v.fetchMu.Lock()
	defer v.fetchMu.Unlock()
	return v.refreshLocked(force)
}

// refreshLocked reads the key set; fetchMu must be held.
func (v *Verifier) refreshLocked(force bool) error {
	if !force && v.now().Sub(v.lastTry) < minInterval {
		return nil // read recently; whatever it found is current
	}
	v.lastTry = v.now()
	keys, err := v.fetch()
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.keys, v.fetched = keys, v.now()
	v.mu.Unlock()
	return nil
}

type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (v *Verifier) fetch() (map[string]any, error) {
	resp, err := v.client.Get(v.url)
	if err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks: HTTP %d", resp.StatusCode)
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	keys := map[string]any{}
	for _, k := range set.Keys {
		if k.Kid == "" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		key, err := parseKey(k)
		if err != nil {
			continue // a key the server cannot use does not invalidate the others
		}
		keys[k.Kid] = key
	}
	if len(keys) == 0 {
		return nil, errors.New("jwks: no usable signing key")
	}
	return keys, nil
}

func parseKey(k jwk) (any, error) {
	b64 := func(s string) ([]byte, error) { return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "=")) }
	switch k.Kty {
	case "RSA":
		if k.Alg != "" && k.Alg != "RS256" {
			return nil, fmt.Errorf("alg %s", k.Alg)
		}
		n, err := b64(k.N)
		if err != nil {
			return nil, err
		}
		e, err := b64(k.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("bad exponent")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < 2048 {
			return nil, errors.New("RSA key shorter than 2048 bits")
		}
		return pub, nil
	case "EC":
		if k.Crv != "P-256" || (k.Alg != "" && k.Alg != "ES256") {
			return nil, fmt.Errorf("curve %s alg %s", k.Crv, k.Alg)
		}
		x, err := b64(k.X)
		if err != nil || len(x) != 32 {
			return nil, errors.New("bad x")
		}
		y, err := b64(k.Y)
		if err != nil || len(y) != 32 {
			return nil, errors.New("bad y")
		}
		return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	}
	return nil, fmt.Errorf("key type %s", k.Kty)
}
