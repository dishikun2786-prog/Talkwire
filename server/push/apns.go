package push

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// APNs endpoints.
const (
	APNsProduction = "https://api.push.apple.com"
	APNsSandbox    = "https://api.sandbox.push.apple.com"
)

// apnsMaxPayload is Apple's limit for a regular remote notification.
const apnsMaxPayload = 4096

// apnsTokenAge is how long a provider token is reused. Apple rejects a token
// older than an hour and throttles providers that mint a new one more often
// than every 20 minutes.
const apnsTokenAge = 40 * time.Minute

// APNsConfig is an Apple token-based connection.
type APNsConfig struct {
	KeyPath string // the .p8 key downloaded from Apple
	KeyID   string
	TeamID  string
	Topic   string // the app's bundle ID
	BaseURL string // APNsProduction or APNsSandbox
	Timeout time.Duration
}

// APNsSender sends through the APNs HTTP/2 API.
type APNsSender struct {
	key    *ecdsa.PrivateKey
	keyID  string
	teamID string
	topic  string
	base   string
	client *http.Client
	now    func() time.Time

	mu       sync.Mutex
	jwt      string
	jwtIssue time.Time
}

// NewAPNs loads the key and returns the sender.
func NewAPNs(c APNsConfig) (*APNsSender, error) {
	if c.KeyID == "" || c.TeamID == "" || c.Topic == "" {
		return nil, errors.New("apns: key ID, team ID and topic (bundle ID) are all required")
	}
	if c.BaseURL == "" {
		c.BaseURL = APNsProduction
	}
	data, err := os.ReadFile(c.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("apns: read key: %w", err)
	}
	key, err := parseECKey(data)
	if err != nil {
		return nil, fmt.Errorf("apns: %w", err)
	}
	// The default transport negotiates HTTP/2 over TLS, which APNs requires.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	return newAPNs(key, c.KeyID, c.TeamID, c.Topic, c.BaseURL, &http.Client{Timeout: c.Timeout, Transport: transport}), nil
}

func newAPNs(key *ecdsa.PrivateKey, keyID, teamID, topic, base string, client *http.Client) *APNsSender {
	return &APNsSender{key: key, keyID: keyID, teamID: teamID, topic: topic, base: base, client: client, now: time.Now}
}

// parseECKey reads a P-256 private key in PKCS#8 (an Apple .p8 file) or SEC 1
// PEM form.
func parseECKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block in the key file")
	}
	var key any
	var err error
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok || ec.Curve != elliptic.P256() {
		return nil, errors.New("the key is not a P-256 EC key")
	}
	return ec, nil
}

// providerToken returns the cached provider token, minting a new one when it
// is old enough.
func (a *APNsSender) providerToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	if a.jwt != "" && now.Sub(a.jwtIssue) < apnsTokenAge {
		return a.jwt, nil
	}
	t := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"iss": a.teamID, "iat": now.Unix()})
	t.Header["kid"] = a.keyID
	s, err := t.SignedString(a.key)
	if err != nil {
		return "", err
	}
	a.jwt, a.jwtIssue = s, now
	return s, nil
}

// dropProviderToken forgets a token APNs rejected, so the next send mints a
// new one.
func (a *APNsSender) dropProviderToken() {
	a.mu.Lock()
	a.jwt = ""
	a.mu.Unlock()
}

type apnsAlert struct {
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Body     string `json:"body,omitempty"`
	LocKey   string `json:"loc-key,omitempty"`
}

type apnsAps struct {
	Alert          apnsAlert `json:"alert"`
	Sound          string    `json:"sound"`
	ThreadID       string    `json:"thread-id,omitempty"`
	MutableContent int       `json:"mutable-content"`
}

// apnsPayload renders n. The routing keys of Notification.Data are top-level
// custom keys next to "aps". mutable-content lets the app's notification
// service extension fetch the avatar.
func apnsPayload(n Notification) ([]byte, error) {
	al := n.alert()
	p := map[string]any{"aps": apnsAps{
		Alert:          apnsAlert(al),
		Sound:          "default",
		ThreadID:       n.Topic,
		MutableContent: 1,
	}}
	for k, v := range n.Data() {
		p[k] = v
	}
	b, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if len(b) > apnsMaxPayload {
		if smaller, ok := n.withoutAvatar(); ok {
			return apnsPayload(smaller)
		}
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}

// Send delivers n to one device.
func (a *APNsSender) Send(ctx context.Context, deviceToken string, n Notification) error {
	body, err := apnsPayload(n)
	if err != nil {
		return err
	}
	bearer, err := a.providerToken()
	if err != nil {
		return fmt.Errorf("apns: sign provider token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+"/3/device/"+deviceToken, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("apns: create request: %w", err)
	}
	req.Header.Set("authorization", "bearer "+bearer)
	req.Header.Set("apns-topic", a.topic)
	req.Header.Set("apns-push-type", "alert")
	req.Header.Set("apns-priority", "10")
	// Stored for a device that is offline, up to a day.
	req.Header.Set("apns-expiration", strconv.FormatInt(a.now().Add(24*time.Hour).Unix(), 10))
	req.Header.Set("content-type", "application/json")
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("apns: send request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	var e struct {
		Reason string `json:"reason"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	_ = json.Unmarshal(raw, &e)
	switch {
	case resp.StatusCode == http.StatusGone, e.Reason == "BadDeviceToken", e.Reason == "Unregistered":
		return ErrTokenGone
	case e.Reason == "ExpiredProviderToken", e.Reason == "InvalidProviderToken":
		a.dropProviderToken()
	case e.Reason == "PayloadTooLarge":
		return ErrPayloadTooLarge
	}
	return fmt.Errorf("apns: HTTP %d %s", resp.StatusCode, e.Reason)
}
