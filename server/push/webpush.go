package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Web Push limits (RFC 8291 section 4): a push service accepts at least
// 4096 bytes of body, which is one record of the 86-byte header, the
// ciphertext and its 16-byte tag, and the plaintext ends with a 1-byte
// delimiter.
const (
	webPushRecordSize   = 4096
	webPushHeaderSize   = 16 + 4 + 1 + 65
	webPushMaxPlaintext = webPushRecordSize - webPushHeaderSize - 16 - 1
)

// vapidLifetime is how long a VAPID token is valid. RFC 8292 allows at most
// 24 hours; a token is reused until an hour before it expires.
const vapidLifetime = 12 * time.Hour

// WebPushConfig is the server's VAPID identity and the push services it may
// call.
type WebPushConfig struct {
	KeyPath string // PEM P-256 private key; see cmd/vapidkey
	Subject string // mailto: or https: contact for the push services
	Hosts   Hosts
	TTL     time.Duration // how long a push service keeps an undelivered message
	Timeout time.Duration
}

// WebPushSender sends through the Web Push protocol (RFC 8030).
type WebPushSender struct {
	key     *ecdsa.PrivateKey
	public  []byte // uncompressed point
	subject string
	hosts   Hosts
	ttl     time.Duration
	client  *http.Client
	now     func() time.Time

	mu    sync.Mutex
	vapid map[string]vapidToken // by audience
}

type vapidToken struct {
	jwt     string
	expires time.Time
}

// NewWebPush loads the VAPID key and returns the sender.
func NewWebPush(c WebPushConfig) (*WebPushSender, error) {
	data, err := os.ReadFile(c.KeyPath)
	if err != nil {
		return nil, fmt.Errorf("webpush: read VAPID key: %w", err)
	}
	key, err := parseECKey(data)
	if err != nil {
		return nil, fmt.Errorf("webpush: VAPID key: %w", err)
	}
	if c.Hosts == nil {
		c.Hosts = DefaultWebPushHosts
	}
	client := &http.Client{
		Timeout: c.Timeout,
		// A redirect would let a push service (or whoever answers at an
		// allowed host) point the server at any URL.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	return newWebPush(key, c.Subject, c.Hosts, c.TTL, client)
}

func newWebPush(key *ecdsa.PrivateKey, subject string, hosts Hosts, ttl time.Duration, client *http.Client) (*WebPushSender, error) {
	pub, err := key.PublicKey.ECDH()
	if err != nil {
		return nil, fmt.Errorf("webpush: VAPID key: %w", err)
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &WebPushSender{
		key: key, public: pub.Bytes(), subject: subject, hosts: hosts, ttl: ttl,
		client: client, now: time.Now, vapid: map[string]vapidToken{},
	}, nil
}

// PublicKey is the VAPID public key browsers subscribe with
// (applicationServerKey), base64url.
func (w *WebPushSender) PublicKey() string {
	return base64.RawURLEncoding.EncodeToString(w.public)
}

// authorization is the RFC 8292 header for an endpoint.
func (w *WebPushSender) authorization(endpoint *url.URL) (string, error) {
	aud := endpoint.Scheme + "://" + endpoint.Host
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	t, ok := w.vapid[aud]
	if !ok || now.Add(time.Hour).After(t.expires) {
		claims := jwt.MapClaims{"aud": aud, "exp": now.Add(vapidLifetime).Unix()}
		if w.subject != "" {
			claims["sub"] = w.subject
		}
		s, err := jwt.NewWithClaims(jwt.SigningMethodES256, claims).SignedString(w.key)
		if err != nil {
			return "", err
		}
		t = vapidToken{jwt: s, expires: now.Add(vapidLifetime)}
		w.vapid[aud] = t
	}
	return "vapid t=" + t.jwt + ", k=" + w.PublicKey(), nil
}

// webPushPayload is what the service worker receives.
type webPushPayload struct {
	Title  string            `json:"title,omitempty"`
	Body   string            `json:"body,omitempty"`
	LocKey string            `json:"locKey,omitempty"`
	Data   map[string]string `json:"data"`
}

func webPushPlaintext(n Notification) ([]byte, error) {
	al := n.alert()
	title := al.Title
	if al.Subtitle != "" {
		title += " - " + al.Subtitle
	}
	b, err := json.Marshal(webPushPayload{Title: title, Body: al.Body, LocKey: al.LocKey, Data: n.Data()})
	if err != nil {
		return nil, err
	}
	if len(b) > webPushMaxPlaintext {
		if smaller, ok := n.withoutAvatar(); ok {
			return webPushPlaintext(smaller)
		}
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}

// Send delivers n to one subscription.
func (w *WebPushSender) Send(ctx context.Context, token string, n Notification) error {
	sub, err := parseSubscription(token)
	if err != nil {
		return err
	}
	endpoint, _ := url.Parse(sub.Endpoint)
	// Checked again here, not only at registration: the allow-list may have
	// been narrowed since the subscription was stored.
	if !w.hosts.Allow(endpoint) {
		return fmt.Errorf("webpush: push service %q is not allowed", endpoint.Hostname())
	}
	plaintext, err := webPushPlaintext(n)
	if err != nil {
		return err
	}
	asPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := encryptWebPush(plaintext, sub.uaPublic, sub.authSecret, asPriv, salt)
	if err != nil {
		return err
	}
	auth, err := w.authorization(endpoint)
	if err != nil {
		return fmt.Errorf("webpush: sign VAPID token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webpush: create request: %w", err)
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", strconv.Itoa(int(w.ttl/time.Second)))
	req.Header.Set("Urgency", "high")
	resp, err := w.client.Do(req)
	if err != nil {
		return fmt.Errorf("webpush: send request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK, http.StatusAccepted:
		return nil
	case http.StatusNotFound, http.StatusGone:
		return ErrTokenGone
	case http.StatusRequestEntityTooLarge:
		return ErrPayloadTooLarge
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("webpush: HTTP %d: %s", resp.StatusCode, raw)
}

// webPushKeys are the values RFC 8291 section 3.4 derives.
type webPushKeys struct {
	prkKey, ikm, prk, cek, nonce []byte
}

// deriveWebPushKeys runs the RFC 8291 key derivation from the ECDH secret
// both sides compute.
func deriveWebPushKeys(ecdhSecret, authSecret, uaPublic, asPublic, salt []byte) (webPushKeys, error) {
	var k webPushKeys
	var err error
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPublic...), asPublic...)
	if k.prkKey, err = hkdf.Extract(sha256.New, ecdhSecret, authSecret); err != nil {
		return k, err
	}
	if k.ikm, err = hkdf.Expand(sha256.New, k.prkKey, string(keyInfo), 32); err != nil {
		return k, err
	}
	if k.prk, err = hkdf.Extract(sha256.New, k.ikm, salt); err != nil {
		return k, err
	}
	if k.cek, err = hkdf.Expand(sha256.New, k.prk, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return k, err
	}
	k.nonce, err = hkdf.Expand(sha256.New, k.prk, "Content-Encoding: nonce\x00", 12)
	return k, err
}

// encryptWebPush encrypts one record (RFC 8291 with the aes128gcm content
// coding of RFC 8188). The ephemeral key and the salt are parameters so the
// RFC's example can be reproduced.
func encryptWebPush(plaintext, uaPublic, authSecret []byte, asPriv *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(plaintext) > webPushMaxPlaintext {
		return nil, ErrPayloadTooLarge
	}
	if len(salt) != 16 {
		return nil, errors.New("webpush: salt must be 16 bytes")
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, err
	}
	secret, err := asPriv.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := asPriv.PublicKey().Bytes()
	k, err := deriveWebPushKeys(secret, authSecret, uaPublic, asPublic, salt)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(k.cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	header := make([]byte, 0, webPushHeaderSize)
	header = append(header, salt...)
	header = binary.BigEndian.AppendUint32(header, webPushRecordSize)
	header = append(header, byte(len(asPublic)))
	header = append(header, asPublic...)
	// The last (and only) record ends with the delimiter 0x02 and no padding.
	record := append(append([]byte{}, plaintext...), 0x02)
	return gcm.Seal(header, k.nonce, record, nil), nil
}
