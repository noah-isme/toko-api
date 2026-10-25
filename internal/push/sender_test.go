package push

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// browserSubscription returns subscription keys like a browser would.
func browserSubscription(t *testing.T, endpoint string) dbgen.PushSubscription {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return dbgen.PushSubscription{
		ID:       newID(),
		UserID:   newID(),
		Endpoint: endpoint,
		P256dh:   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}
}

type vapidClaims struct {
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Exp int64  `json:"exp"`
}

func decodeVAPIDHeader(t *testing.T, header string) (vapidClaims, string) {
	t.Helper()
	require.True(t, strings.HasPrefix(header, "vapid t="), header)
	parts := strings.SplitN(strings.TrimPrefix(header, "vapid t="), ", k=", 2)
	require.Len(t, parts, 2)
	segments := strings.Split(parts[0], ".")
	require.Len(t, segments, 3)
	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	require.NoError(t, err)
	var claims vapidClaims
	require.NoError(t, json.Unmarshal(raw, &claims))
	return claims, parts[1]
}

func TestWebPushSenderDeliversEncryptedPayloadWithValidVAPID(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)

	for _, subject := range []string{"mailto:no-reply@example.com", "no-reply@example.com"} {
		t.Run(subject, func(t *testing.T) {
			var got struct {
				claims vapidClaims
				key    string
				enc    string
				ttl    string
				body   []byte
			}
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got.claims, got.key = decodeVAPIDHeader(t, r.Header.Get("Authorization"))
				got.enc = r.Header.Get("Content-Encoding")
				got.ttl = r.Header.Get("TTL")
				got.body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()

			sender := NewWebPushSender(pub, priv, subject, 3600, srv.Client())
			msg := Message{Title: "Pembayaran diterima", Body: "plaintext-marker"}
			status, err := sender.Send(context.Background(), browserSubscription(t, srv.URL+"/push/token"), msg)
			require.NoError(t, err)
			require.Equal(t, http.StatusCreated, status)

			require.Equal(t, "mailto:no-reply@example.com", got.claims.Sub, "JWT sub must not be mailto:mailto:")
			require.Equal(t, srv.URL, got.claims.Aud)
			require.Greater(t, got.claims.Exp, time.Now().Unix())
			require.Equal(t, pub, got.key)
			require.Equal(t, "aes128gcm", got.enc)
			require.Equal(t, "3600", got.ttl)
			require.NotEmpty(t, got.body)
			require.NotContains(t, string(got.body), "plaintext-marker", "payload must be encrypted")
		})
	}
}

func TestWebPushSenderReturnsStatusOnRejection(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()

	status, err := NewWebPushSender(pub, priv, "mailto:a@b.c", 60, srv.Client()).
		Send(context.Background(), browserSubscription(t, srv.URL+"/gone"), Message{Title: "x"})
	require.NoError(t, err)
	require.Equal(t, http.StatusGone, status)
}

// A dial refused by the SSRF guard depends on how the worker resolves an
// allowlisted push host, not on the subscription: never prune, never retry.
func TestWebPushSenderRefusedAddressIsNoRetryNotPermanent(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	sender := NewWebPushSender(pub, priv, "mailto:a@b.c", 60, common.NewOutboundHTTPClient(2*time.Second, false))
	_, err = sender.Send(context.Background(), browserSubscription(t, srv.URL+"/secret-token"), Message{Title: "x"})
	var de *DeliveryError
	require.True(t, errors.As(err, &de), "got %v", err)
	require.False(t, de.Permanent, "a refused address must not prune the subscription")
	require.True(t, de.NoRetry)
	require.ErrorIs(t, err, common.ErrUnsafeAddress)
	require.NotContains(t, err.Error(), "secret-token")
	require.Zero(t, hits.Load())
}

func TestWebPushSenderInvalidKeysArePermanent(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	sub := dbgen.PushSubscription{Endpoint: "https://fcm.googleapis.com/fcm/send/secret-token", P256dh: "bm90LWEta2V5", Auth: "c2hvcnQ"}
	_, err = NewWebPushSender(pub, priv, "mailto:a@b.c", 60, common.NewOutboundHTTPClient(time.Second, false)).
		Send(context.Background(), sub, Message{Title: "x"})
	var de *DeliveryError
	require.True(t, errors.As(err, &de), "got %v", err)
	require.True(t, de.Permanent)
	require.Equal(t, "fcm.googleapis.com", de.Host)
	require.NotContains(t, err.Error(), "secret-token")
}

func TestWebPushSenderTransportErrorIsRetryableAndRedacted(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL + "/secret-token"
	client := srv.Client()
	srv.Close() // connection refused from now on

	_, err = NewWebPushSender(pub, priv, "mailto:a@b.c", 60, client).
		Send(context.Background(), browserSubscription(t, endpoint), Message{Title: "x"})
	var de *DeliveryError
	require.True(t, errors.As(err, &de), "got %v", err)
	require.False(t, de.Permanent)
	require.NotContains(t, err.Error(), "secret-token")
}

func TestNewWebPushSenderPanicsWithoutClient(t *testing.T) {
	require.Panics(t, func() { NewWebPushSender("a", "b", "mailto:c@d.e", 1, nil) })
}

// A misconfigured server key (standard base64, quotes, garbage) must never be
// mistaken for bad subscription keys: that would prune every subscription.
func TestWebPushSenderMalformedServerKeyIsNotPermanent(t *testing.T) {
	_, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	for name, keys := range map[string][2]string{
		"private key in standard base64": {pub, "abc+/def+/ghi="},
		"quoted private key":             {pub, `"abc"`},
		"public key in standard base64":  {"BPub+Key/x=", "c2VjcmV0"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewWebPushSender(keys[0], keys[1], "mailto:a@b.c", 60, srv.Client()).
				Send(context.Background(), browserSubscription(t, srv.URL+"/secret-token"), Message{Title: "x"})
			var de *DeliveryError
			require.True(t, errors.As(err, &de), "got %v", err)
			require.False(t, de.Permanent, "a server-side key error must not prune")
			require.NotContains(t, err.Error(), "secret-token")
		})
	}
	require.Zero(t, hits.Load(), "no request can be built without a usable VAPID key")
}

func TestWebPushSenderRejectsBadSubscriptionKeysBeforeSending(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	offCurve := make([]byte, 65)
	offCurve[0] = 0x04
	offCurve[64] = 1
	cases := map[string]func(*dbgen.PushSubscription){
		"p256dh not base64":      func(s *dbgen.PushSubscription) { s.P256dh = "!!!" },
		"p256dh off the curve":   func(s *dbgen.PushSubscription) { s.P256dh = base64.RawURLEncoding.EncodeToString(offCurve) },
		"p256dh compressed form": func(s *dbgen.PushSubscription) { s.P256dh = base64.RawURLEncoding.EncodeToString(make([]byte, 33)) },
		"auth wrong length":      func(s *dbgen.PushSubscription) { s.Auth = base64.RawURLEncoding.EncodeToString(make([]byte, 8)) },
		"auth not base64":        func(s *dbgen.PushSubscription) { s.Auth = "@@" },
	}
	sender := NewWebPushSender(pub, priv, "mailto:a@b.c", 60, srv.Client())
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			sub := browserSubscription(t, srv.URL+"/secret-token")
			mutate(&sub)
			_, err := sender.Send(context.Background(), sub, Message{Title: "x"})
			var de *DeliveryError
			require.True(t, errors.As(err, &de), "got %v", err)
			require.True(t, de.Permanent)
			require.ErrorIs(t, err, errInvalidSubscriptionKeys)
		})
	}
	require.Zero(t, hits.Load())
}

func TestWebPushSenderAcceptsPaddedStandardBase64SubscriptionKeys(t *testing.T) {
	priv, pub, err := webpush.GenerateVAPIDKeys()
	require.NoError(t, err)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	sub := browserSubscription(t, srv.URL+"/t")
	dh, err := base64.RawURLEncoding.DecodeString(sub.P256dh)
	require.NoError(t, err)
	auth, err := base64.RawURLEncoding.DecodeString(sub.Auth)
	require.NoError(t, err)
	sub.P256dh = base64.StdEncoding.EncodeToString(dh)
	sub.Auth = base64.StdEncoding.EncodeToString(auth)
	status, err := NewWebPushSender(pub, priv, "mailto:a@b.c", 60, srv.Client()).Send(context.Background(), sub, Message{Title: "x"})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
}
