package main

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ErrFCMTokenUnregistered is returned by sendFCMv1 when FCM reports the
// device token as no longer valid (app uninstalled, token rotated on the
// device, or -- the case this exists for -- an old account's token that was
// never explicitly unregistered on logout, see im_fcm_tokens's doc comment
// in store.go). Callers use this to prune the dead row instead of retrying
// it forever.
var ErrFCMTokenUnregistered = errors.New("fcm: token unregistered")

// fcmErrorEnvelope is the subset of FCM v1's google.rpc.Status error body
// needed to distinguish "this token is dead, stop sending to it" from every
// other failure mode (rate limiting, malformed payload, auth failure) that
// must NOT prune a token that might still be good.
type fcmErrorEnvelope struct {
	Error struct {
		Details []struct {
			ErrorCode string `json:"errorCode"`
		} `json:"details"`
	} `json:"error"`
}

// isFCMUnregisteredError inspects the raw FCM v1 error response body for the
// UNREGISTERED error code (https://firebase.google.com/docs/cloud-messaging/manage-tokens).
// Falls back to false (never prune) if the body doesn't parse as expected --
// pruning a token on a guess would be worse than leaving a dead one around.
func isFCMUnregisteredError(body []byte) bool {
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

// ServiceAccountKey represents a Firebase Admin SDK service account key file
type ServiceAccountKey struct {
	Type                    string `json:"type"`
	ProjectID               string `json:"project_id"`
	PrivateKeyID            string `json:"private_key_id"`
	PrivateKey              string `json:"private_key"`
	ClientEmail             string `json:"client_email"`
	ClientID                string `json:"client_id"`
	AuthURI                 string `json:"auth_uri"`
	TokenURI                string `json:"token_uri"`
	AuthProviderX509CertURL string `json:"auth_provider_x509_cert_url"`
	ClientX509CertURL       string `json:"client_x509_cert_url"`
}

// FCMv1Message represents a Firebase Cloud Messaging v1 API message
type FCMv1Message struct {
	Message FCMv1Payload `json:"message"`
}

type FCMv1Payload struct {
	Token   string            `json:"token"`
	Data    map[string]string `json:"data,omitempty"`
	Android *FCMAndroidConfig `json:"android,omitempty"`
}

type FCMAndroidConfig struct {
	Priority string `json:"priority"`
}

// LoadServiceAccountKey reads and parses a Firebase Admin SDK key file
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

// getFCMAccessToken obtains an OAuth2 access token from a Firebase service account
func getFCMAccessToken(saKey *ServiceAccountKey) (string, error) {
	now := time.Now()
	// Create JWT claims
	claims := jwt.MapClaims{
		"iss":   saKey.ClientEmail,
		"scope": "https://www.googleapis.com/auth/firebase.messaging",
		"aud":   "https://oauth2.googleapis.com/token",
		"iat":   now.Unix(),
		"exp":   now.Add(3600 * time.Second).Unix(),
	}

	// Parse the private key
	block, _ := pem.Decode([]byte(saKey.PrivateKey))
	if block == nil {
		return "", fmt.Errorf("failed to decode private key PEM")
	}
	parsedKey, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}
	rsaKey, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return "", fmt.Errorf("private key is not RSA")
	}

	// Create and sign the JWT
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = saKey.PrivateKeyID
	jwtStr, err := token.SignedString(rsaKey)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	// Exchange JWT for access token.
	//
	// F-40 (a): this used to be a bare http.Post, i.e. http.DefaultClient,
	// which has NO timeout at all. A hung TCP connection to Google's token
	// endpoint — the exact failure mode a national firewall or a black-holed
	// route produces, where packets are dropped rather than refused — leaves
	// this call blocked forever, not for 30 seconds.
	body := fmt.Sprintf("grant_type=urn:ietf:params:oauth:grant-type:jwt-bearer&assertion=%s", jwtStr)
	resp, err := fcmOAuthClient.Post(
		"https://oauth2.googleapis.com/token",
		"application/x-www-form-urlencoded",
		strings.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("token exchange decode: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("empty access token in response")
	}

	return tokenResp.AccessToken, nil
}

// fcmHTTPTimeout bounds both the OAuth token exchange and the FCM send.
// Overridable because these calls leave the country and the tolerable wait
// differs per deployment.
var fcmHTTPTimeout = envDuration("IM_FCM_HTTP_TIMEOUT_SEC", 10*time.Second)

// fcmOAuthClient is a package-level client (rather than one per call) so
// connections to Google's token endpoint are pooled and, crucially, so the
// timeout can never be forgotten at a call site the way http.Post's implicit
// DefaultClient invited.
var fcmOAuthClient = &http.Client{Timeout: fcmHTTPTimeout}

// fcmSendClient is the equivalent for the messages:send endpoint. It used to
// be constructed fresh inside sendFCMv1, throwing away the connection pool
// on every single push.
var fcmSendClient = &http.Client{Timeout: fcmHTTPTimeout}

// fcmTokenCache holds the most recently minted FCM OAuth2 access token.
// Without caching, every single push (including every offline member of
// every group message) re-ran a full JWT-sign + HTTPS round trip to
// Google's token endpoint even though the token stays valid for an hour.
//
// refreshAt/usableUntil are two different deadlines on the same token, and
// the gap between them is what lets a refresh happen off the critical path:
// Google issues a 1-hour token, we start refreshing at 55 minutes, and the
// token stays perfectly good for another few minutes while that refresh is
// in flight. Callers arriving during the refresh get the old token instead
// of blocking on it.
var fcmTokenCache struct {
	mu   sync.Mutex
	cond *sync.Cond
	// refreshing marks that exactly one goroutine is out doing the HTTP
	// exchange, so a burst of pushes cannot turn into a burst of identical
	// token requests (and get itself rate-limited by Google).
	refreshing  bool
	token       string
	refreshAt   time.Time
	usableUntil time.Time
}

func init() {
	fcmTokenCache.cond = sync.NewCond(&fcmTokenCache.mu)
}

// getFCMAccessTokenCached returns a cached access token, refreshing ahead of
// the real 1-hour expiry.
//
// F-40 (b): the previous version held fcmTokenCache.mu across the token
// exchange (`defer mu.Unlock()` on the first line, HTTP call in the middle).
// Combined with (a)'s missing timeout that was not merely slow, it was
// unbounded: at the moment the cached token aged out, the first push
// goroutine took the lock and hung on the network, and every subsequent push
// goroutine — one per offline recipient of every message — piled up on that
// same mutex and stayed there. Nothing ever released it, so the goroutine
// count grew for as long as messages kept arriving, until the process died
// of memory exhaustion. A single unreachable Google endpoint took down the
// whole IM server, not just push.
//
// The lock is now held only for the bookkeeping around the call, never
// during it.
func getFCMAccessTokenCached(saKey *ServiceAccountKey) (string, error) {
	c := &fcmTokenCache
	c.mu.Lock()
	for {
		now := time.Now()
		if c.token != "" && now.Before(c.refreshAt) {
			token := c.token
			c.mu.Unlock()
			return token, nil
		}
		if !c.refreshing {
			// Nobody is refreshing; this goroutine takes the job.
			break
		}
		// A refresh is already in flight. If the token we hold is stale but
		// still genuinely accepted by FCM, use it and get out of the way --
		// this is the common path during the 5-minute refresh window.
		if c.token != "" && now.Before(c.usableUntil) {
			token := c.token
			c.mu.Unlock()
			return token, nil
		}
		// Only cold start (no token at all) or a token past even its hard
		// deadline gets here, and waiting is bounded by the refresher's own
		// HTTP timeout.
		c.cond.Wait()
	}
	c.refreshing = true
	c.mu.Unlock()

	token, err := getFCMAccessToken(saKey)

	c.mu.Lock()
	c.refreshing = false
	if err == nil {
		now := time.Now()
		c.token = token
		c.refreshAt = now.Add(55 * time.Minute)
		c.usableUntil = now.Add(58 * time.Minute)
	}
	// Broadcast on the failure path too, or waiters block until the next
	// push happens to come along -- which, if push is what is broken, may be
	// never.
	c.cond.Broadcast()
	c.mu.Unlock()

	if err != nil {
		return "", err
	}
	return token, nil
}

// sendFCMv1 sends a data-only push via Firebase Cloud Messaging v1 API. The
// payload intentionally has no `notification` block: FCM auto-displays
// "notification" messages using default system behavior whenever the app
// isn't in the foreground, bypassing FcmService.onMessageReceived() (and with
// it the custom channel/avatar/tap-to-open logic) entirely. A data-only
// message guarantees onMessageReceived() always runs.
func sendFCMv1(saKey *ServiceAccountKey, deviceToken string, data map[string]string) error {
	accessToken, err := getFCMAccessTokenCached(saKey)
	if err != nil {
		return fmt.Errorf("get access token: %w", err)
	}

	msg := FCMv1Message{
		Message: FCMv1Payload{
			Token: deviceToken,
			Data:  data,
			Android: &FCMAndroidConfig{
				Priority: "high",
			},
		},
	}

	jsonBytes, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	url := fmt.Sprintf("https://fcm.googleapis.com/v1/projects/%s/messages:send", saKey.ProjectID)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(jsonBytes)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := fcmSendClient.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		if isFCMUnregisteredError(respBody) {
			return ErrFCMTokenUnregistered
		}
		return fmt.Errorf("FCM API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
