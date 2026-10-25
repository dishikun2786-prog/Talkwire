// Package push sends offline notifications through Firebase Cloud Messaging
// (Android), the Apple Push Notification service (iOS) and Web Push
// (browsers).
//
// A Notification is built once and each provider renders it:
//
//   - FCM: a data-only message whose data map is Notification.Data, the same
//     map the server always sent, so existing Android clients keep working.
//   - APNs: an alert (title, subtitle and body, or a localisation key) in
//     "aps", plus Notification.Data as top-level custom keys.
//   - Web Push: the JSON object {"title", "body", "locKey", "data"} (empty
//     fields omitted), encrypted for the browser (RFC 8291) and signed with
//     the server's VAPID key (RFC 8292). The service worker displays it.
//
// An end-to-end encrypted message carries neither its sender nor a preview:
// the server never has the plaintext, and a push service must not learn even
// who wrote to whom. Its alert is the localisation key LocKeyE2eMessage, which
// the app resolves to its own "new encrypted message" text.
package push

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Providers, as stored with each token.
const (
	FCM     = "fcm"
	APNs    = "apns"
	WebPush = "webpush"
)

// Kinds of notification.
const (
	// KindMessage is a new message (the zero value).
	KindMessage = ""
	// KindE2eMessage is a queued end-to-end encrypted envelope.
	KindE2eMessage = "e2e_message"
	// KindGroupJoin is a join request for a group manager, or the review of
	// one for the requester.
	KindGroupJoin = "group_join"
)

// LocKeyE2eMessage is the localisation key an encrypted message is announced
// with on APNs and Web Push.
const LocKeyE2eMessage = "IMKIT_E2E_MESSAGE"

var (
	// ErrTokenGone means the provider reported the token as no longer valid
	// (app uninstalled, token rotated, subscription expired). The caller
	// should forget it.
	ErrTokenGone = errors.New("push: token no longer valid")
	// ErrPayloadTooLarge means the notification does not fit the provider's
	// limit even without the sender's avatar.
	ErrPayloadTooLarge = errors.New("push: payload too large")
	// ErrNotConfigured means the token's provider is not configured on this
	// server. Registration does not depend on configuration, so this is
	// expected for some devices and not worth logging.
	ErrNotConfigured = errors.New("push: provider not configured")
)

// Token is one registered device.
type Token struct {
	Provider string
	Value    string
}

// Notification is what a push says, independent of the provider.
type Notification struct {
	Kind string
	// Topic is the conversation as the recipient's client keys it.
	Topic   string
	IsGroup bool
	// SenderName and SenderAvatar describe the author of a message.
	SenderName   string
	SenderAvatar string
	GroupName    string
	// Body is the preview of a message or the sentence of a notice.
	Body string
}

// Data is the key-value form of the notification: the whole FCM payload, and
// the custom keys of the other providers. An encrypted message keeps only
// the routing keys whatever else is set.
func (n Notification) Data() map[string]string {
	d := map[string]string{"topic": n.Topic, "isGroup": strconv.FormatBool(n.IsGroup)}
	switch n.Kind {
	case KindE2eMessage:
		d["type"] = KindE2eMessage
		d["e2e"] = "true"
	case KindGroupJoin:
		d["type"] = KindGroupJoin
		d["groupName"] = n.GroupName
		d["preview"] = n.Body
	default:
		d["senderName"] = n.SenderName
		d["preview"] = n.Body
		if n.SenderAvatar != "" {
			d["senderAvatar"] = n.SenderAvatar
		}
		if n.IsGroup && n.GroupName != "" {
			d["groupName"] = n.GroupName
		}
	}
	return d
}

// withoutAvatar is the fallback when a payload is over a provider's limit:
// the avatar URL is the only field of unbounded length.
func (n Notification) withoutAvatar() (Notification, bool) {
	if n.SenderAvatar == "" {
		return n, false
	}
	n.SenderAvatar = ""
	return n, true
}

// alert is what a platform that displays the notification itself shows.
type alert struct {
	Title, Subtitle, Body, LocKey string
}

func (n Notification) alert() alert {
	switch n.Kind {
	case KindE2eMessage:
		return alert{LocKey: LocKeyE2eMessage}
	case KindGroupJoin:
		return alert{Title: n.GroupName, Body: n.Body}
	default:
		if n.IsGroup && n.GroupName != "" {
			return alert{Title: n.GroupName, Subtitle: n.SenderName, Body: n.Body}
		}
		return alert{Title: n.SenderName, Body: n.Body}
	}
}

// Sender delivers notifications through one provider.
type Sender interface {
	Send(ctx context.Context, token string, n Notification) error
}

// Dispatcher routes each token to its provider's sender.
type Dispatcher struct {
	senders map[string]Sender
}

// NewDispatcher returns a dispatcher for the configured providers, or nil
// when there are none.
func NewDispatcher(senders map[string]Sender) *Dispatcher {
	d := &Dispatcher{senders: map[string]Sender{}}
	for p, s := range senders {
		if s != nil {
			d.senders[p] = s
		}
	}
	if len(d.senders) == 0 {
		return nil
	}
	return d
}

// Providers lists the configured providers.
func (d *Dispatcher) Providers() []string {
	var out []string
	for p := range d.senders {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Send delivers n to one device.
func (d *Dispatcher) Send(ctx context.Context, t Token, n Notification) error {
	s := d.senders[t.Provider]
	if s == nil {
		return ErrNotConfigured
	}
	return s.Send(ctx, t.Value, n)
}

// NormalizeToken checks a token a client registers and returns its provider
// and the form to store. An empty provider means FCM, the only one older
// clients know. FCM tokens are stored as given, as they always were.
func NormalizeToken(provider, raw string, webPushHosts Hosts) (string, string, error) {
	switch provider {
	case "", FCM:
		return FCM, raw, nil
	case APNs:
		t := strings.ToLower(raw)
		if len(t) < 64 || len(t) > 200 || len(t)%2 != 0 {
			return "", "", errors.New("push: an APNs device token is 64 to 200 hex digits")
		}
		if _, err := hex.DecodeString(t); err != nil {
			return "", "", errors.New("push: an APNs device token is 64 to 200 hex digits")
		}
		return APNs, t, nil
	case WebPush:
		sub, err := parseSubscription(raw)
		if err != nil {
			return "", "", err
		}
		u, _ := url.Parse(sub.Endpoint)
		if !webPushHosts.Allow(u) {
			return "", "", fmt.Errorf("push: web push service %q is not allowed", u.Hostname())
		}
		return WebPush, sub.String(), nil
	default:
		return "", "", fmt.Errorf("push: unknown provider %q", provider)
	}
}

// subscription is a browser's PushSubscription (its toJSON form; other
// fields such as expirationTime are ignored).
type subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`

	uaPublic   []byte
	authSecret []byte
}

func parseSubscription(raw string) (*subscription, error) {
	var s subscription
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, errors.New("push: a web push token is a PushSubscription in JSON")
	}
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("push: the subscription endpoint must be an https URL")
	}
	if s.uaPublic, err = decodeB64(s.Keys.P256dh); err != nil {
		return nil, errors.New("push: keys.p256dh is not base64url")
	}
	if _, err := ecdh.P256().NewPublicKey(s.uaPublic); err != nil {
		return nil, errors.New("push: keys.p256dh is not an uncompressed P-256 point")
	}
	if s.authSecret, err = decodeB64(s.Keys.Auth); err != nil || len(s.authSecret) != 16 {
		return nil, errors.New("push: keys.auth is not 16 bytes of base64url")
	}
	return &s, nil
}

// String is the canonical stored form, so a client that registers and later
// unregisters the same subscription matches the stored row byte for byte.
func (s *subscription) String() string {
	c := map[string]any{
		"endpoint": s.Endpoint,
		"keys": map[string]string{
			"p256dh": base64.RawURLEncoding.EncodeToString(s.uaPublic),
			"auth":   base64.RawURLEncoding.EncodeToString(s.authSecret),
		},
	}
	b, _ := json.Marshal(c)
	return string(b)
}

// decodeB64 accepts base64url with or without padding, as browsers and
// libraries differ.
func decodeB64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// Hosts is the allow-list of push services the server may POST to. A Web
// Push endpoint comes from the client, so without a list any client could
// make the server send requests to any URL. "host" matches exactly,
// "*.domain" matches any subdomain of domain, and "*" alone matches any host
// on any port.
type Hosts []string

// DefaultWebPushHosts are the push services of Chrome and Edge, Firefox,
// Safari and legacy Edge.
var DefaultWebPushHosts = Hosts{
	"fcm.googleapis.com",
	"updates.push.services.mozilla.com",
	"web.push.apple.com",
	"*.notify.windows.com",
}

// ParseHosts reads a comma-separated list.
func ParseHosts(s string) Hosts {
	var out Hosts
	for _, h := range strings.Split(s, ",") {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// Allow reports whether the server may POST to u.
func (hs Hosts) Allow(u *url.URL) bool {
	if u == nil || u.Scheme != "https" || u.User != nil || u.Hostname() == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	for _, h := range hs {
		if h == "*" {
			return true
		}
	}
	if p := u.Port(); p != "" && p != "443" {
		return false
	}
	for _, h := range hs {
		if suffix, ok := strings.CutPrefix(h, "*."); ok {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == h {
			return true
		}
	}
	return false
}
