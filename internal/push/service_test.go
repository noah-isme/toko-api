package push

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/config"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	fcmEndpoint     = "https://fcm.googleapis.com/fcm/send/secret-token-1"
	mozillaEndpoint = "https://updates.push.services.mozilla.com/wpush/v2/secret-token-2"
)

func enabledService(store *stubStore, sender Sender) *Service {
	return &Service{Q: store, Sender: sender, PublicKey: "pub", AllowedHosts: config.DefaultPushEndpointAllowedHosts, MaxSubscriptions: 10}
}

func TestSendToUserDisabled(t *testing.T) {
	svc := &Service{Q: &stubStore{}}
	_, err := svc.SendToUser(context.Background(), newID(), Message{})
	require.ErrorIs(t, err, ErrPushDisabled)
}

func TestSendToUserNoSubscriptions(t *testing.T) {
	svc := enabledService(&stubStore{}, &fakeSender{})
	_, err := svc.SendToUser(context.Background(), newID(), Message{})
	require.ErrorIs(t, err, ErrNoSubscriptions)
}

func TestSendToUserSendsToEverySubscription(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint), subFor(user, mozillaEndpoint), subFor(newID(), fcmEndpoint+"x")}}
	sender := &fakeSender{}
	res, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{Title: "t"})
	require.NoError(t, err)
	require.Equal(t, SendResult{Sent: 2}, res)
	require.Len(t, sender.sent, 2)
}

func TestSendToUserPrunesGoneSubscriptions(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint), subFor(user, mozillaEndpoint)}}
	sender := &fakeSender{statuses: map[string]int{fcmEndpoint: 410, mozillaEndpoint: 404}}
	res, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{})
	require.NoError(t, err)
	require.Equal(t, SendResult{Pruned: 2}, res)
	require.Len(t, store.deleted, 2)
	require.Equal(t, user, store.deleted[0].UserID)
}

func TestSendToUserPrunesNonHTTPSEndpointWithoutSending(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, "http://10.0.0.5/x")}}
	sender := &fakeSender{}
	res, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{})
	require.NoError(t, err)
	require.Equal(t, SendResult{Pruned: 1}, res)
	require.Empty(t, sender.sent)
	require.Equal(t, "http://10.0.0.5/x", store.deleted[0].Endpoint)
}

func TestSendToUserPrunesPermanentDeliveryErrors(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint)}}
	sender := &fakeSender{errs: map[string]error{fcmEndpoint: &DeliveryError{Host: "fcm.googleapis.com", Class: "invalid subscription keys", Permanent: true}}}
	res, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{})
	require.NoError(t, err)
	require.Equal(t, SendResult{Pruned: 1}, res)
}

func TestSendToUserRetryableFailureReturnsError(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint), subFor(user, mozillaEndpoint)}}
	sender := &fakeSender{
		statuses: map[string]int{fcmEndpoint: 503},
		errs:     map[string]error{mozillaEndpoint: &DeliveryError{Host: "updates.push.services.mozilla.com", Class: "timeout", cause: errTransport}},
	}
	res, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{})
	require.Error(t, err)
	require.Equal(t, SendResult{Failed: 2}, res)
	require.Empty(t, store.deleted)
	// Redaction: hosts only, never the endpoint path (the subscription token).
	require.NotContains(t, err.Error(), "secret-token")
	require.Contains(t, err.Error(), "fcm.googleapis.com")
}

func TestSendToUserForeignSenderErrorIsRedacted(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint)}}
	sender := &fakeSender{errs: map[string]error{fcmEndpoint: errors.New(`Post "` + fcmEndpoint + `": EOF`)}}
	_, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-token")
}

func TestSendToUserPermanent4xxDropped(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint)}}
	sender := &fakeSender{statuses: map[string]int{fcmEndpoint: 400}}
	res, err := enabledService(store, sender).SendToUser(context.Background(), user, Message{})
	require.NoError(t, err)
	require.Equal(t, SendResult{Failed: 1}, res)
	require.Empty(t, store.deleted)
}

func TestCreateSubscriptionRejectsInvalidEndpoint(t *testing.T) {
	store := &stubStore{}
	svc := enabledService(store, nil)
	_, err := svc.CreateSubscription(context.Background(), newID(), "https://evil.example.com/x", "p", "a")
	require.ErrorIs(t, err, ErrInvalidEndpoint)
	require.Empty(t, store.created)
}

func TestCreateSubscriptionTrimsToMax(t *testing.T) {
	store := &stubStore{}
	user := newID()
	svc := enabledService(store, nil)
	_, err := svc.CreateSubscription(context.Background(), user, fcmEndpoint, "p", "a")
	require.NoError(t, err)
	require.Equal(t, []string{"create", "trim"}, store.callOrder)
	require.Equal(t, dbgen.TrimPushSubscriptionsParams{UserID: user, Offset: 10}, store.trimmed[0])
}

func TestAllowedDefaultsTrueWithoutPreferences(t *testing.T) {
	svc := enabledService(&stubStore{}, nil)
	ok, err := svc.Allowed(context.Background(), newID(), CategoryOrderUpdate)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestAllowedRespectsMasterAndTypeToggles(t *testing.T) {
	store := &stubStore{prefs: &dbgen.PushPreference{Enabled: true, OrderUpdates: false, PromoUpdates: true}}
	svc := enabledService(store, nil)
	ok, err := svc.Allowed(context.Background(), newID(), CategoryOrderUpdate)
	require.NoError(t, err)
	require.False(t, ok)
	ok, _ = svc.Allowed(context.Background(), newID(), CategoryPromoUpdates)
	require.True(t, ok)

	store.prefs = &dbgen.PushPreference{Enabled: false, OrderUpdates: true, PromoUpdates: true, StockUpdates: true}
	ok, _ = svc.Allowed(context.Background(), newID(), CategoryOrderUpdate)
	require.False(t, ok)

	store.prefsErr = errors.New("db down")
	_, err = svc.Allowed(context.Background(), newID(), CategoryOrderUpdate)
	require.Error(t, err)
}

func TestEnabledRequiresSenderAndKey(t *testing.T) {
	require.False(t, (&Service{PublicKey: "k"}).Enabled())
	require.False(t, (&Service{Sender: &fakeSender{}}).Enabled())
	require.True(t, (&Service{Sender: &fakeSender{}, PublicKey: "k"}).Enabled())
	var nilSvc *Service
	require.False(t, nilSvc.Enabled())
	require.True(t, strings.HasPrefix(PushDeliveryTask(), "push-"))
}
