package push

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type apiError struct {
	Error struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Details json.RawMessage `json:"details"`
	} `json:"error"`
}

func authed(r *http.Request, user pgtype.UUID) *http.Request {
	return r.WithContext(common.WithUserID(r.Context(), uuidString(user)))
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) apiError {
	t.Helper()
	var body apiError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return body
}

func subscribeBody(endpoint string) *bytes.Reader {
	raw, _ := json.Marshal(map[string]any{"endpoint": endpoint, "keys": map[string]string{"p256dh": "p", "auth": "a"}})
	return bytes.NewReader(raw)
}

func TestGetVapidPublicKeyDisabledReturns503(t *testing.T) {
	h := &Handler{Svc: &Service{Q: &stubStore{}}}
	rec := httptest.NewRecorder()
	h.GetVapidPublicKey(rec, authed(httptest.NewRequest(http.MethodGet, "/push/vapid-key", nil), newID()))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "PUSH_DISABLED", decodeErr(t, rec).Error.Code)
}

func TestGetVapidPublicKeyReturnsBothKeyFields(t *testing.T) {
	h := &Handler{Svc: &Service{Q: &stubStore{}, Sender: &fakeSender{}, PublicKey: "BPUBKEY"}}
	rec := httptest.NewRecorder()
	h.GetVapidPublicKey(rec, authed(httptest.NewRequest(http.MethodGet, "/push/vapid-key", nil), newID()))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"public_key":"BPUBKEY","publicKey":"BPUBKEY"}`, rec.Body.String())
}

func TestSubscribeRejectsInvalidEndpoints(t *testing.T) {
	for name, endpoint := range map[string]string{
		"http":         "http://fcm.googleapis.com/x",
		"ip literal":   "https://10.0.0.1/x",
		"unknown host": "https://evil.example.com/secret-token",
	} {
		t.Run(name, func(t *testing.T) {
			store := &stubStore{}
			h := &Handler{Svc: enabledService(store, &fakeSender{})}
			rec := httptest.NewRecorder()
			h.Subscribe(rec, authed(httptest.NewRequest(http.MethodPost, "/push/subscription", subscribeBody(endpoint)), newID()))
			require.Equal(t, http.StatusBadRequest, rec.Code)
			body := decodeErr(t, rec)
			require.Equal(t, "INVALID_ENDPOINT", body.Error.Code)
			require.NotContains(t, rec.Body.String(), "secret-token")
			require.Empty(t, store.created)
		})
	}
}

func TestSubscribeRejectsOversizedFields(t *testing.T) {
	h := &Handler{Svc: enabledService(&stubStore{}, &fakeSender{})}
	rec := httptest.NewRecorder()
	h.Subscribe(rec, authed(httptest.NewRequest(http.MethodPost, "/push/subscription",
		subscribeBody("https://fcm.googleapis.com/"+strings.Repeat("a", 2100))), newID()))
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "INVALID_BODY", decodeErr(t, rec).Error.Code)
}

func TestSubscribeAcceptsAllowlistedEndpoint(t *testing.T) {
	store := &stubStore{}
	user := newID()
	h := &Handler{Svc: enabledService(store, &fakeSender{})}
	rec := httptest.NewRecorder()
	h.Subscribe(rec, authed(httptest.NewRequest(http.MethodPost, "/push/subscription", subscribeBody(fcmEndpoint)), user))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, store.created, 1)
	require.Equal(t, user, store.created[0].UserID)
	require.Len(t, store.trimmed, 1)
}

func TestSubscribeWorksWhilePushDisabled(t *testing.T) {
	// Subscriptions can be stored before VAPID keys are configured.
	store := &stubStore{}
	h := &Handler{Svc: &Service{Q: store, AllowedHosts: []string{"fcm.googleapis.com"}}}
	rec := httptest.NewRecorder()
	h.Subscribe(rec, authed(httptest.NewRequest(http.MethodPost, "/push/subscription", subscribeBody(fcmEndpoint)), newID()))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSendTestDisabledReturns503(t *testing.T) {
	h := &Handler{Svc: &Service{Q: &stubStore{}}}
	rec := httptest.NewRecorder()
	h.SendTest(rec, authed(httptest.NewRequest(http.MethodPost, "/push/send-test", nil), newID()))
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Equal(t, "PUSH_DISABLED", decodeErr(t, rec).Error.Code)
}

func TestSendTestNoSubscriptionReturns404(t *testing.T) {
	h := &Handler{Svc: enabledService(&stubStore{}, &fakeSender{})}
	rec := httptest.NewRecorder()
	h.SendTest(rec, authed(httptest.NewRequest(http.MethodPost, "/push/send-test", nil), newID()))
	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "NO_SUBSCRIPTION", decodeErr(t, rec).Error.Code)
}

func TestSendTestAllFailedReturns502(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint), subFor(user, mozillaEndpoint)}}
	sender := &fakeSender{statuses: map[string]int{fcmEndpoint: 503, mozillaEndpoint: 410}}
	h := &Handler{Svc: enabledService(store, sender)}
	rec := httptest.NewRecorder()
	h.SendTest(rec, authed(httptest.NewRequest(http.MethodPost, "/push/send-test", nil), user))
	require.Equal(t, http.StatusBadGateway, rec.Code)
	body := decodeErr(t, rec)
	require.Equal(t, "PUSH_SEND_FAILED", body.Error.Code)
	require.JSONEq(t, `{"sent":0,"failed":1,"pruned":1}`, string(body.Error.Details))
}

func TestSendTestSuccessReportsCounts(t *testing.T) {
	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint), subFor(user, mozillaEndpoint)}}
	sender := &fakeSender{statuses: map[string]int{mozillaEndpoint: 404}}
	h := &Handler{Svc: enabledService(store, sender)}
	rec := httptest.NewRecorder()
	h.SendTest(rec, authed(httptest.NewRequest(http.MethodPost, "/push/send-test", nil), user))
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"success":true,"message":"Test notification sent","sent":1,"failed":0,"pruned":1}`, rec.Body.String())
	require.Equal(t, "Notifikasi uji", sender.sent[0].msg.Title)
	require.Equal(t, "/account/notifications", sender.sent[0].msg.Data["url"])
}

func TestSendTestIgnoresPreferences(t *testing.T) {
	user := newID()
	store := &stubStore{
		subs:  []dbgen.PushSubscription{subFor(user, fcmEndpoint)},
		prefs: &dbgen.PushPreference{Enabled: false},
	}
	h := &Handler{Svc: enabledService(store, &fakeSender{})}
	rec := httptest.NewRecorder()
	h.SendTest(rec, authed(httptest.NewRequest(http.MethodPost, "/push/send-test", nil), user))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestSendTestUnauthorized(t *testing.T) {
	h := &Handler{Svc: enabledService(&stubStore{}, &fakeSender{})}
	rec := httptest.NewRecorder()
	h.SendTest(rec, httptest.NewRequest(http.MethodPost, "/push/send-test", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, "UNAUTHORIZED", decodeErr(t, rec).Error.Code)
}
