package push

import (
	"context"
	"encoding/json"
	"errors"
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
	// Permanent failures cannot succeed on retry; the subscription is pruned.
	Permanent bool
	cause     error
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

func classifySendErr(host string, err error) *DeliveryError {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		// Failed before any request was made: the stored p256dh/auth keys do
		// not decode or are not a valid curve point.
		return &DeliveryError{Host: host, Class: "invalid subscription keys", Permanent: true, cause: err}
	}
	if errors.Is(err, common.ErrUnsafeAddress) {
		return &DeliveryError{Host: host, Class: "endpoint resolves to a non-public address", Permanent: true, cause: err}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &DeliveryError{Host: host, Class: "timeout", cause: err}
	}
	return &DeliveryError{Host: host, Class: "network error", cause: err}
}
