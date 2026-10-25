package push

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

var (
	// ErrPushDisabled is returned when no VAPID keys are configured.
	ErrPushDisabled = errors.New("push: web push is not configured")
	// ErrNoSubscriptions is returned when the user has no stored subscription.
	ErrNoSubscriptions = errors.New("push: user has no subscriptions")
)

// Message is the JSON payload the storefront service worker receives
// (event.data.json()).
type Message struct {
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Tag   string         `json:"tag,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
}

// Sender delivers one message to one subscription and returns the push
// service's HTTP status. A non-nil error means no response was received.
type Sender interface {
	Send(ctx context.Context, sub dbgen.PushSubscription, msg Message) (int, error)
}

// DeliveryError describes a push attempt that got no HTTP response. Its
// message names only the push service host and a failure class, never the
// endpoint URL (whose path is the subscription's bearer token).
type DeliveryError struct {
	Host  string
	Class string
	// Permanent failures prove the subscription itself is unusable; the
	// subscription is pruned.
	Permanent bool
	// NoRetry failures cannot succeed on retry, but say nothing about the
	// subscription (for example the worker's own DNS resolves the push host to
	// an address the SSRF guard refuses). They are counted as failed and logged;
	// the subscription is kept and the job is not retried.
	NoRetry bool
	cause   error
}

func (e *DeliveryError) Error() string {
	return "push: " + e.Class + " sending to " + e.Host
}

// Unwrap exposes the cause for errors.Is checks. Do not print the cause: a
// *url.Error contains the full endpoint URL.
func (e *DeliveryError) Unwrap() error { return e.cause }

// WebPushSender sends VAPID-signed, aes128gcm-encrypted pushes via webpush-go.
type WebPushSender struct {
	PublicKey  string
	PrivateKey string
	// Subject is "mailto:<addr>" or "https://..." (normalised by config).
	Subject    string
	TTL        int
	HTTPClient *http.Client
}

// NewWebPushSender builds a sender. client must be the SSRF-safe client from
// common.NewOutboundHTTPClient; a nil client panics so the requirement cannot
// be skipped by accident (webpush-go would fall back to a bare http.Client).
func NewWebPushSender(publicKey, privateKey, subject string, ttl int, client *http.Client) *WebPushSender {
	if client == nil {
		panic("push: NewWebPushSender requires an outbound HTTP client")
	}
	return &WebPushSender{PublicKey: publicKey, PrivateKey: privateKey, Subject: subject, TTL: ttl, HTTPClient: client}
}

// Send implements Sender.
func (s *WebPushSender) Send(ctx context.Context, sub dbgen.PushSubscription, msg Message) (int, error) {
	host := endpointHost(sub.Endpoint)
	if s == nil || s.HTTPClient == nil {
		return 0, &DeliveryError{Host: host, Class: "sender not configured"}
	}
	if err := checkSubscriptionKeys(sub); err != nil {
		return 0, &DeliveryError{Host: host, Class: "invalid subscription keys", Permanent: true, cause: err}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return 0, err
	}
	resp, err := webpush.SendNotificationWithContext(ctx, body, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys:     webpush.Keys{Auth: sub.Auth, P256dh: sub.P256dh},
	}, &webpush.Options{
		HTTPClient: s.HTTPClient,
		// webpush-go prepends "mailto:" to any subscriber that is not an https
		// URL, so pass the bare address; otherwise the JWT sub would read
		// "mailto:mailto:..." and Apple's push service rejects it.
		Subscriber:      strings.TrimPrefix(s.Subject, "mailto:"),
		VAPIDPublicKey:  s.PublicKey,
		VAPIDPrivateKey: s.PrivateKey,
		TTL:             s.TTL,
	})
	if err != nil {
		return 0, classifySendErr(host, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

// classifySendErr maps a webpush-go error. Subscription keys were checked
// before the call, so an error without an HTTP request (not a *url.Error) is a
// server-side problem, such as an unusable VAPID key, a JWT signing failure or
// a payload the library cannot encrypt. Those are never Permanent: pruning on
// them would delete every valid subscription because of the server's own
// misconfiguration.
//
// A dial refused by the SSRF guard (common.ErrUnsafeAddress) is not Permanent
// either. Stored endpoints are allowlisted DNS names, so the refused address
// comes from how this worker's resolver answers (DNS64/NAT64 prefixes, service
// mesh VIPs, fake-IP egress proxies), not from the subscription. It is NoRetry:
// the next attempt resolves the same way.
func classifySendErr(host string, err error) *DeliveryError {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return &DeliveryError{Host: host, Class: "push request could not be built (server-side error: VAPID keys or payload)", cause: err}
	}
	if errors.Is(err, common.ErrUnsafeAddress) {
		return &DeliveryError{Host: host, Class: "push host resolves to an address the outbound guard refuses", NoRetry: true, cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &DeliveryError{Host: host, Class: "timeout", cause: err}
	}
	return &DeliveryError{Host: host, Class: "network error", cause: err}
}

// errInvalidSubscriptionKeys is the cause of a Permanent "invalid subscription
// keys" DeliveryError. It never contains the key material.
var errInvalidSubscriptionKeys = errors.New("push: invalid subscription keys")

// checkSubscriptionKeys validates the browser-supplied keys the same way
// webpush-go decodes them (standard or URL-safe base64, padding optional):
// p256dh must be an uncompressed point on P-256 and auth a 16-byte secret
// (RFC 8291 section 3). Only a failure here proves the subscription itself is
// unusable.
func checkSubscriptionKeys(sub dbgen.PushSubscription) error {
	dh, err := decodeSubscriptionKey(sub.P256dh)
	if err != nil {
		return fmt.Errorf("%w: p256dh is not base64", errInvalidSubscriptionKeys)
	}
	if _, err := ecdh.P256().NewPublicKey(dh); err != nil {
		return fmt.Errorf("%w: p256dh is not an uncompressed P-256 point", errInvalidSubscriptionKeys)
	}
	auth, err := decodeSubscriptionKey(sub.Auth)
	if err != nil {
		return fmt.Errorf("%w: auth is not base64", errInvalidSubscriptionKeys)
	}
	if len(auth) != 16 {
		return fmt.Errorf("%w: auth must be 16 bytes, got %d", errInvalidSubscriptionKeys, len(auth))
	}
	return nil
}

// decodeSubscriptionKey mirrors webpush-go's decoder so this check accepts
// exactly what the library would.
func decodeSubscriptionKey(key string) ([]byte, error) {
	if rem := len(key) % 4; rem != 0 {
		key += strings.Repeat("=", 4-rem)
	}
	if b, err := base64.StdEncoding.DecodeString(key); err == nil {
		return b, nil
	}
	return base64.URLEncoding.DecodeString(key)
}
