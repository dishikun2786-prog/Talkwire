package push

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ServiceAccountKey is a Firebase Admin SDK service account key file.
type ServiceAccountKey struct {
	Type         string `json:"type"`
	ProjectID    string `json:"project_id"`
	PrivateKeyID string `json:"private_key_id"`
	PrivateKey   string `json:"private_key"`
	ClientEmail  string `json:"client_email"`
	TokenURI     string `json:"token_uri"`
}

// LoadServiceAccountKey reads and parses a Firebase Admin SDK key file.
func LoadServiceAccountKey(path string) (*ServiceAccountKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read service account key: %w", err)
	}
	var key ServiceAccountKey
	if err := json.Unmarshal(data, &key); err != nil {
		return nil, fmt.Errorf("parse service account key: %w", err)
	}
	return &key, nil
}

// FCMSender sends through the FCM HTTP v1 API.
type FCMSender struct {
	key      *ServiceAccountKey
	rsaKey   *rsa.PrivateKey
	client   *http.Client
	tokenURL string
	sendURL  string
	cache    fcmTokenCache
}

// NewFCM checks the key and returns the sender. timeout bounds both the
// OAuth token exchange and each send.
//
// F-40 (a): the token exchange used to be a bare http.Post, i.e.
// http.DefaultClient, which has no timeout at all. A hung TCP connection to
// Google's token endpoint -- the exact failure a national firewall or a
// black-holed route produces, where packets are dropped rather than refused
// -- left the call blocked forever. One client with a timeout, shared by
// both calls, also keeps the connections pooled.
func NewFCM(key *ServiceAccountKey, timeout time.Duration) (*FCMSender, error) {
	if key.ProjectID == "" || key.ClientEmail == "" {
		return nil, errors.New("fcm: the service account key has no project_id or client_email")
	}
	block, _ := pem.Decode([]byte(key.PrivateKey))
	if block == nil {
		return nil, errors.New("fcm: failed to decode private key PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("fcm: parse private key: %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("fcm: private key is not RSA")
	}
	f := &FCMSender{
		key:      key,
		rsaKey:   rsaKey,
		client:   &http.Client{Timeout: timeout},
		tokenURL: "https://oauth2.googleapis.com/token",
		sendURL:  fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", url.PathEscape(key.ProjectID)),
	}
	f.cache.cond = sync.NewCond(&f.cache.mu)
	return f, nil
}

type fcmMessage struct {
	Message fcmPayload `json:"message"`
}

type fcmPayload struct {
	Token   string            `json:"token"`
	Data    map[string]string `json:"data,omitempty"`
	Android *fcmAndroidConfig `json:"android,omitempty"`
}

type fcmAndroidConfig struct {
	Priority string `json:"priority"`
}

// Send sends a data-only message. The payload intentionally has no
// "notification" block: FCM displays "notification" messages itself with
// the default system behaviour whenever the app isn't in the foreground,
// bypassing the app's FcmService.onMessageReceived (and with it the custom
// channel, avatar and tap-to-open logic). A data-only message guarantees
// onMessageReceived always runs.
func (f *FCMSender) Send(ctx context.Context, deviceToken string, n Notification) error {
	accessToken, err := f.accessToken(ctx)
	if err != nil {
		return fmt.Errorf("fcm: get access token: %w", err)
	}
	body, err := json.Marshal(fcmMessage{Message: fcmPayload{
		Token:   deviceToken,
		Data:    n.Data(),
		Android: &fcmAndroidConfig{Priority: "high"},
	}})
	if err != nil {
		return fmt.Errorf("fcm: marshal message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.sendURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("fcm: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fcm: send request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 400 {
		if isFCMUnregistered(respBody) {
			return ErrTokenGone
		}
		return fmt.Errorf("fcm: API error %d: %s", resp.StatusCode, respBody)
	}
	return nil
}

// fcmErrorEnvelope is the part of FCM v1's google.rpc.Status error body that
// tells "this token is dead, stop sending to it" from every other failure
// (rate limiting, malformed payload, auth failure), which must not prune a
// token that may still be good.
type fcmErrorEnvelope struct {
	Error struct {
		Details []struct {
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// isFCMUnregistered looks for the UNREGISTERED error code
// (https://firebase.google.com/docs/cloud-messaging/manage-tokens). A body
// that doesn't parse means "don't prune": pruning a token on a guess would
// be worse than keeping a dead one.
func isFCMUnregistered(body []byte) bool {
	var env fcmErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return false
	}
	for _, d := range env.Error.Details {
		if d.ErrorCode == "UNREGISTERED" {
			return true
		}
	}
	return false
}

// fcmTokenCache holds the most recently minted OAuth2 access token. Without
// it every push re-ran a JWT signature and an HTTPS round trip to Google
// although the token stays valid for an hour.
//
// refreshAt and usableUntil are two deadlines on the same token, and the gap
// between them lets a refresh happen off the critical path: Google issues a
// 1-hour token, the refresh starts at 55 minutes, and the token stays good
// for a few more minutes while it is in flight. Callers arriving during the
// refresh get the old token instead of waiting.
type fcmTokenCache struct {
	mu   sync.Mutex
	cond *sync.Cond
	// refreshing marks that exactly one goroutine is doing the exchange, so
	// a burst of pushes cannot turn into a burst of identical token requests
	// (and get itself rate-limited by Google).
	refreshing  bool
	token       string
	refreshAt   time.Time
	usableUntil time.Time
}

// accessToken returns a cached access token, refreshing it ahead of the real
// expiry.
//
// F-40 (b): an earlier version held the cache lock across the token
// exchange. Together with (a)'s missing timeout that was unbounded: when the
// cached token aged out, the first push took the lock and hung on the
// network, and every later push -- one per offline recipient of every
// message -- piled up on the same lock and stayed there until the process
// ran out of memory. The lock is now held only for the bookkeeping around
// the call, never during it.
func (f *FCMSender) accessToken(ctx context.Context) (string, error) {
	c := &f.cache
	c.mu.Lock()
	for {
		now := time.Now()
		if c.token != "" && now.Before(c.refreshAt) {
			token := c.token
			c.mu.Unlock()
			return token, nil
		}
		if !c.refreshing {
			break // nobody is refreshing; this goroutine takes the job
		}
		// A refresh is in flight. A token that is stale but still accepted
		// is good enough -- the common path during the refresh window.
		if c.token != "" && now.Before(c.usableUntil) {
			token := c.token
			c.mu.Unlock()
			return token, nil
		}
		// Cold start, or a token past even its hard deadline: wait, bounded
		// by the refresher's own HTTP timeout.
		c.cond.Wait()
	}
	c.refreshing = true
	c.mu.Unlock()

	token, err := f.exchange(ctx)

	c.mu.Lock()
	c.refreshing = false
	if err == nil {
		now := time.Now()
		c.token = token
		c.refreshAt = now.Add(55 * time.Minute)
		c.usableUntil = now.Add(58 * time.Minute)
	}
	// Broadcast on failure too, or waiters block until the next push comes
	// along -- which, if push is what is broken, may be never.
	c.cond.Broadcast()
	c.mu.Unlock()
	return token, err
}

// exchange trades a signed JWT for an access token.
func (f *FCMSender) exchange(ctx context.Context) (string, error) {
	now := time.Now()
	jt := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   f.key.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	})
	jt.Header["kid"] = f.key.PrivateKeyID
	assertion, err := jt.SignedString(f.rsaKey)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.tokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()
	var tr struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&tr); err != nil {
		return "", fmt.Errorf("token exchange decode (HTTP %d): %w", resp.StatusCode, err)
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("token exchange: no access token (HTTP %d)", resp.StatusCode)
	}
	return tr.AccessToken, nil
}
