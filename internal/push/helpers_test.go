package push

import (
	"context"
	"errors"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// stubStore is an in-memory Store.
type stubStore struct {
	mu        sync.Mutex
	subs      []dbgen.PushSubscription
	prefs     *dbgen.PushPreference
	prefsErr  error
	listErr   error
	created   []dbgen.CreatePushSubscriptionParams
	deleted   []dbgen.DeletePushSubscriptionParams
	trimmed   []dbgen.TrimPushSubscriptionsParams
	callOrder []string
}

func (s *stubStore) GetPushSubscription(context.Context, dbgen.GetPushSubscriptionParams) (dbgen.PushSubscription, error) {
	return dbgen.PushSubscription{}, pgx.ErrNoRows
}

func (s *stubStore) GetPushSubscriptionsByUser(_ context.Context, userID pgtype.UUID) ([]dbgen.PushSubscription, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	var out []dbgen.PushSubscription
	for _, sub := range s.subs {
		if sub.UserID == userID {
			out = append(out, sub)
		}
	}
	return out, nil
}

func (s *stubStore) CreatePushSubscription(_ context.Context, arg dbgen.CreatePushSubscriptionParams) (dbgen.PushSubscription, error) {
	s.created = append(s.created, arg)
	s.callOrder = append(s.callOrder, "create")
	return dbgen.PushSubscription{ID: newID(), UserID: arg.UserID, Endpoint: arg.Endpoint, P256dh: arg.P256dh, Auth: arg.Auth}, nil
}

func (s *stubStore) DeletePushSubscription(_ context.Context, arg dbgen.DeletePushSubscriptionParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, arg)
	return nil
}

func (s *stubStore) DeleteAllPushSubscriptions(context.Context, pgtype.UUID) error { return nil }

func (s *stubStore) GetPushPreferences(context.Context, pgtype.UUID) (dbgen.PushPreference, error) {
	if s.prefsErr != nil {
		return dbgen.PushPreference{}, s.prefsErr
	}
	if s.prefs == nil {
		return dbgen.PushPreference{}, pgx.ErrNoRows
	}
	return *s.prefs, nil
}

func (s *stubStore) UpsertPushPreferences(_ context.Context, arg dbgen.UpsertPushPreferencesParams) (dbgen.PushPreference, error) {
	return dbgen.PushPreference{UserID: arg.UserID, Enabled: arg.Enabled, OrderUpdates: arg.OrderUpdates, PromoUpdates: arg.PromoUpdates, StockUpdates: arg.StockUpdates}, nil
}

func (s *stubStore) TrimPushSubscriptions(_ context.Context, arg dbgen.TrimPushSubscriptionsParams) error {
	s.trimmed = append(s.trimmed, arg)
	s.callOrder = append(s.callOrder, "trim")
	return nil
}

// fakeSender returns a scripted status (or error) per endpoint.
type fakeSender struct {
	mu       sync.Mutex
	statuses map[string]int
	errs     map[string]error
	sent     []sentPush
}

type sentPush struct {
	sub dbgen.PushSubscription
	msg Message
}

func (f *fakeSender) Send(_ context.Context, sub dbgen.PushSubscription, msg Message) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentPush{sub: sub, msg: msg})
	if err := f.errs[sub.Endpoint]; err != nil {
		return 0, err
	}
	if status, ok := f.statuses[sub.Endpoint]; ok {
		return status, nil
	}
	return 201, nil
}

func newID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

func subFor(userID pgtype.UUID, endpoint string) dbgen.PushSubscription {
	return dbgen.PushSubscription{ID: newID(), UserID: userID, Endpoint: endpoint, P256dh: "p", Auth: "a"}
}

var errTransport = errors.New("transport")
