package notify_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/notify"
	"github.com/noah-isme/backend-toko/internal/resilience"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

func toUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func uuidString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return uuid.UUID(u.Bytes).String()
}

func TestSignatureAndHeaders(t *testing.T) {
	type recorded struct {
		req  *http.Request
		body []byte
	}
	received := make(chan recorded, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- recorded{req: r, body: body}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	dispatcher := &notify.Dispatcher{
		HTTP: &resilience.HTTPClient{
			Client:      srv.Client(),
			Breaker:     resilience.NewBreaker(1, 1, time.Second),
			MaxAttempts: 1,
			Timeout:     time.Second,
			Target:      "webhook-delivery",
		},
		Enabled: true,
	}
	endpoint := dbgen.WebhookEndpoint{Url: srv.URL, Secret: "secret", ID: toUUID(uuid.New())}
	event := dbgen.DomainEvent{
		ID:         toUUID(uuid.New()),
		Topic:      "order.paid",
		Payload:    []byte(`{"id":1}`),
		OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	delivery := dbgen.WebhookDelivery{ID: toUUID(uuid.New())}

	status, _, err := dispatcher.Deliver(context.Background(), endpoint, event, delivery)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)

	record := <-received
	req := record.req
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, uuidString(event.ID), req.Header.Get("X-Event-ID"))
	require.Equal(t, uuidString(delivery.ID), req.Header.Get("X-Idempotency-Key"))
	timestamp := req.Header.Get("X-Timestamp")
	require.NotEmpty(t, timestamp)
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	require.NoError(t, err)
	bodyBytes := record.body
	require.Equal(t, notify.ComputeSignature(endpoint.Secret, ts, req.Header.Get("X-Event-ID"), bodyBytes), req.Header.Get("X-Signature"))
}

type retryStore struct {
	attempt  int
	endpoint dbgen.WebhookEndpoint
	event    dbgen.DomainEvent
	failed   []dbgen.MarkFailedWithBackoffParams
	dlq      []dbgen.MoveToDLQParams
}

func (r *retryStore) CreateWebhookEndpoint(context.Context, dbgen.CreateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, errors.New("not implemented")
}

func (r *retryStore) UpdateWebhookEndpoint(context.Context, dbgen.UpdateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, errors.New("not implemented")
}

func (r *retryStore) GetWebhookEndpoint(context.Context, pgtype.UUID) (dbgen.WebhookEndpoint, error) {
	return r.endpoint, nil
}

func (r *retryStore) ListWebhookEndpoints(context.Context, dbgen.ListWebhookEndpointsParams) ([]dbgen.WebhookEndpoint, error) {
	return nil, nil
}

func (r *retryStore) DeleteWebhookEndpoint(context.Context, dbgen.DeleteWebhookEndpointParams) (int64, error) {
	return 0, nil
}

func (r *retryStore) ListActiveEndpointsForTopicAndTenant(context.Context, dbgen.ListActiveEndpointsForTopicAndTenantParams) ([]dbgen.WebhookEndpoint, error) {
	return nil, nil
}

func (r *retryStore) EnqueueDelivery(context.Context, dbgen.EnqueueDeliveryParams) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, nil
}

func (r *retryStore) DequeueDueDeliveries(context.Context, int32) ([]dbgen.WebhookDelivery, error) {
	if r.attempt > 1 {
		return nil, nil
	}
	delivery := dbgen.WebhookDelivery{
		ID:         toUUID(uuid.New()),
		EndpointID: r.endpoint.ID,
		EventID:    r.event.ID,
		Attempt:    int32(r.attempt),
		MaxAttempt: 2,
	}
	return []dbgen.WebhookDelivery{delivery}, nil
}

func (r *retryStore) MarkDelivering(context.Context, pgtype.UUID) error { return nil }

func (r *retryStore) MarkDelivered(context.Context, dbgen.MarkDeliveredParams) error { return nil }

func (r *retryStore) MarkFailedWithBackoff(_ context.Context, arg dbgen.MarkFailedWithBackoffParams) error {
	r.failed = append(r.failed, arg)
	r.attempt++
	return nil
}

func (r *retryStore) MoveToDLQ(_ context.Context, arg dbgen.MoveToDLQParams) error {
	r.dlq = append(r.dlq, arg)
	r.attempt++
	return nil
}

func (r *retryStore) InsertWebhookDlq(context.Context, dbgen.InsertWebhookDlqParams) (dbgen.WebhookDlq, error) {
	return dbgen.WebhookDlq{}, nil
}

func (r *retryStore) GetDeliveryByID(context.Context, pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, errors.New("not implemented")
}

func (r *retryStore) GetDeliveryByEndpointEvent(context.Context, dbgen.GetDeliveryByEndpointEventParams) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, errors.New("not implemented")
}

func (r *retryStore) ResetDeliveryForReplay(context.Context, pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, errors.New("not implemented")
}

func (r *retryStore) DeleteDlqByDelivery(context.Context, pgtype.UUID) error { return nil }

func (r *retryStore) ListWebhookDeliveries(context.Context, dbgen.ListWebhookDeliveriesParams) ([]dbgen.ListWebhookDeliveriesRow, error) {
	return nil, nil
}

func (r *retryStore) CountWebhookDeliveries(context.Context, dbgen.CountWebhookDeliveriesParams) (int64, error) {
	return 0, nil
}

func (r *retryStore) GetDomainEvent(context.Context, pgtype.UUID) (dbgen.DomainEvent, error) {
	return r.event, nil
}

func TestRetryAndDLQ(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	store := &retryStore{
		endpoint: dbgen.WebhookEndpoint{ID: toUUID(uuid.New()), Url: srv.URL, Secret: "secret"},
		event:    dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.paid", Payload: []byte(`{"id":1}`), OccurredAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}},
	}

	dispatcher := &notify.Dispatcher{
		Store: store,
		HTTP: &resilience.HTTPClient{
			Client:      srv.Client(),
			Breaker:     resilience.NewBreaker(1, 1, time.Second),
			MaxAttempts: 1,
			Timeout:     time.Second,
			Target:      "webhook-delivery",
		},
		BackoffBaseSec:     3,
		DefaultMaxAttempts: 2,
		Enabled:            true,
	}

	require.NoError(t, dispatcher.WorkOnce(context.Background(), 1))
	require.Len(t, store.failed, 1)
	require.Equal(t, int32(3), store.failed[0].DelaySec)

	require.NoError(t, dispatcher.WorkOnce(context.Background(), 1))
	require.Len(t, store.dlq, 1)
}

type scheduleStore struct {
	endpoints     []dbgen.WebhookEndpoint
	enqueued      int
	enqueuedFor   []pgtype.UUID
	askedTenants  []pgtype.UUID
	duplicateOnce bool
}

func (s *scheduleStore) CreateWebhookEndpoint(context.Context, dbgen.CreateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, nil
}

func (s *scheduleStore) UpdateWebhookEndpoint(context.Context, dbgen.UpdateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, nil
}

func (s *scheduleStore) GetWebhookEndpoint(context.Context, pgtype.UUID) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, nil
}

func (s *scheduleStore) ListWebhookEndpoints(context.Context, dbgen.ListWebhookEndpointsParams) ([]dbgen.WebhookEndpoint, error) {
	return nil, nil
}

func (s *scheduleStore) DeleteWebhookEndpoint(context.Context, dbgen.DeleteWebhookEndpointParams) (int64, error) {
	return 0, nil
}

func (s *scheduleStore) ListActiveEndpointsForTopicAndTenant(_ context.Context, arg dbgen.ListActiveEndpointsForTopicAndTenantParams) ([]dbgen.WebhookEndpoint, error) {
	s.askedTenants = append(s.askedTenants, arg.TenantID)
	out := make([]dbgen.WebhookEndpoint, 0, len(s.endpoints))
	for _, ep := range s.endpoints {
		if ep.TenantID == arg.TenantID {
			out = append(out, ep)
		}
	}
	return out, nil
}

func (s *scheduleStore) EnqueueDelivery(_ context.Context, arg dbgen.EnqueueDeliveryParams) (dbgen.WebhookDelivery, error) {
	s.enqueued++
	s.enqueuedFor = append(s.enqueuedFor, arg.EndpointID)
	if s.duplicateOnce && s.enqueued == 1 {
		return dbgen.WebhookDelivery{}, &pgconn.PgError{Code: "23505"}
	}
	return dbgen.WebhookDelivery{ID: toUUID(uuid.New()), MaxAttempt: arg.MaxAttempt}, nil
}

func (s *scheduleStore) DequeueDueDeliveries(context.Context, int32) ([]dbgen.WebhookDelivery, error) {
	return nil, nil
}
func (s *scheduleStore) MarkDelivering(context.Context, pgtype.UUID) error              { return nil }
func (s *scheduleStore) MarkDelivered(context.Context, dbgen.MarkDeliveredParams) error { return nil }
func (s *scheduleStore) MarkFailedWithBackoff(context.Context, dbgen.MarkFailedWithBackoffParams) error {
	return nil
}
func (s *scheduleStore) MoveToDLQ(context.Context, dbgen.MoveToDLQParams) error { return nil }
func (s *scheduleStore) InsertWebhookDlq(context.Context, dbgen.InsertWebhookDlqParams) (dbgen.WebhookDlq, error) {
	return dbgen.WebhookDlq{}, nil
}
func (s *scheduleStore) GetDeliveryByID(context.Context, pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, nil
}
func (s *scheduleStore) GetDeliveryByEndpointEvent(context.Context, dbgen.GetDeliveryByEndpointEventParams) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{ID: toUUID(uuid.New()), Status: dbgen.DeliveryStatusDELIVERED}, nil
}
func (s *scheduleStore) ResetDeliveryForReplay(context.Context, pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, nil
}
func (s *scheduleStore) DeleteDlqByDelivery(context.Context, pgtype.UUID) error { return nil }
func (s *scheduleStore) ListWebhookDeliveries(context.Context, dbgen.ListWebhookDeliveriesParams) ([]dbgen.ListWebhookDeliveriesRow, error) {
	return nil, nil
}
func (s *scheduleStore) CountWebhookDeliveries(context.Context, dbgen.CountWebhookDeliveriesParams) (int64, error) {
	return 0, nil
}
func (s *scheduleStore) GetDomainEvent(context.Context, pgtype.UUID) (dbgen.DomainEvent, error) {
	return dbgen.DomainEvent{}, nil
}

func newScheduleDispatcher(store *scheduleStore) *notify.Dispatcher {
	return &notify.Dispatcher{
		Store: store,
		HTTP: &resilience.HTTPClient{
			Client:      http.DefaultClient,
			Breaker:     resilience.NewBreaker(1, 1, time.Second),
			MaxAttempts: 1,
			Timeout:     time.Second,
			Target:      "webhook-delivery",
		},
		Enabled: true,
	}
}

func TestScheduleFiltersEndpointsByTenant(t *testing.T) {
	tenantA := toUUID(uuid.New())
	tenantB := toUUID(uuid.New())
	epA := dbgen.WebhookEndpoint{ID: toUUID(uuid.New()), TenantID: tenantA}
	epB := dbgen.WebhookEndpoint{ID: toUUID(uuid.New()), TenantID: tenantB}
	store := &scheduleStore{endpoints: []dbgen.WebhookEndpoint{epA, epB}}
	dispatcher := newScheduleDispatcher(store)
	event := dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "payment.expired"}

	ctx := tenant.WithTenant(context.Background(), uuidString(tenantA))
	require.NoError(t, dispatcher.Schedule(ctx, event))
	require.Equal(t, []pgtype.UUID{tenantA}, store.askedTenants)
	require.Equal(t, []pgtype.UUID{epA.ID}, store.enqueuedFor)
}

func TestScheduleSkipsWithoutTenant(t *testing.T) {
	store := &scheduleStore{endpoints: []dbgen.WebhookEndpoint{{ID: toUUID(uuid.New()), TenantID: toUUID(uuid.New())}}}
	dispatcher := newScheduleDispatcher(store)
	event := dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.created"}

	require.NoError(t, dispatcher.Schedule(context.Background(), event))
	require.Empty(t, store.askedTenants)
	require.Zero(t, store.enqueued)

	// A tenant value that is not a UUID is treated like a missing tenant.
	require.NoError(t, dispatcher.Schedule(tenant.WithTenant(context.Background(), "not-a-uuid"), event))
	require.Zero(t, store.enqueued)
}

func TestIdempotencyUniqueDelivery(t *testing.T) {
	tenantID := toUUID(uuid.New())
	store := &scheduleStore{duplicateOnce: true, endpoints: []dbgen.WebhookEndpoint{{ID: toUUID(uuid.New()), TenantID: tenantID}, {ID: toUUID(uuid.New()), TenantID: tenantID}}}
	dispatcher := &notify.Dispatcher{
		Store: store,
		HTTP: &resilience.HTTPClient{
			Client:      http.DefaultClient,
			Breaker:     resilience.NewBreaker(1, 1, time.Second),
			MaxAttempts: 1,
			Timeout:     time.Second,
			Target:      "webhook-delivery",
		},
		Enabled: true,
	}
	event := dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.created"}

	err := dispatcher.Schedule(tenant.WithTenant(context.Background(), uuidString(tenantID)), event)
	require.NoError(t, err)
	require.Equal(t, 2, store.enqueued)
}

func TestScheduleUsesEventTenantBeforeContext(t *testing.T) {
	eventTenant, ctxTenant := uuid.New(), uuid.New()
	store := &scheduleStore{endpoints: []dbgen.WebhookEndpoint{
		{ID: toUUID(uuid.New()), TenantID: toUUID(eventTenant)},
		{ID: toUUID(uuid.New()), TenantID: toUUID(ctxTenant)},
	}}
	dispatcher := &notify.Dispatcher{Store: store, Enabled: true}

	// Stamped event, no ctx tenant (the outbox relay, the worker).
	require.NoError(t, dispatcher.Schedule(context.Background(), dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "product.price_changed", TenantID: toUUID(eventTenant)}))
	// Stamped event under another tenant's ctx: the event tenant wins.
	require.NoError(t, dispatcher.Schedule(tenant.WithTenant(context.Background(), ctxTenant.String()),
		dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "product.price_changed", TenantID: toUUID(eventTenant)}))
	require.Equal(t, []pgtype.UUID{toUUID(eventTenant), toUUID(eventTenant)}, store.askedTenants)

	// Unstamped event: the ctx tenant is the fallback; with neither nothing is scheduled.
	require.NoError(t, dispatcher.Schedule(tenant.WithTenant(context.Background(), ctxTenant.String()), dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.paid"}))
	require.NoError(t, dispatcher.Schedule(context.Background(), dbgen.DomainEvent{ID: toUUID(uuid.New()), Topic: "order.paid"}))
	require.Equal(t, []pgtype.UUID{toUUID(eventTenant), toUUID(eventTenant), toUUID(ctxTenant)}, store.askedTenants)
}
