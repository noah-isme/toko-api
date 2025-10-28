package notify

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Store defines the persistence operations required for webhook management.
type Store interface {
	CreateWebhookEndpoint(ctx context.Context, arg dbgen.CreateWebhookEndpointParams) (dbgen.WebhookEndpoint, error)
	UpdateWebhookEndpoint(ctx context.Context, arg dbgen.UpdateWebhookEndpointParams) (dbgen.WebhookEndpoint, error)
	GetWebhookEndpoint(ctx context.Context, id pgtype.UUID) (dbgen.WebhookEndpoint, error)
	ListWebhookEndpoints(ctx context.Context, arg dbgen.ListWebhookEndpointsParams) ([]dbgen.WebhookEndpoint, error)
	DeleteWebhookEndpoint(ctx context.Context, id pgtype.UUID) error

	ListActiveEndpointsForTopic(ctx context.Context, topic string) ([]dbgen.WebhookEndpoint, error)
	EnqueueDelivery(ctx context.Context, arg dbgen.EnqueueDeliveryParams) (dbgen.WebhookDelivery, error)
	DequeueDueDeliveries(ctx context.Context, limit int32) ([]dbgen.WebhookDelivery, error)
	MarkDelivering(ctx context.Context, id pgtype.UUID) error
	MarkDelivered(ctx context.Context, arg dbgen.MarkDeliveredParams) error
	MarkFailedWithBackoff(ctx context.Context, arg dbgen.MarkFailedWithBackoffParams) error
	MoveToDLQ(ctx context.Context, arg dbgen.MoveToDLQParams) error
	InsertWebhookDlq(ctx context.Context, arg dbgen.InsertWebhookDlqParams) (dbgen.WebhookDlq, error)
	GetDeliveryByID(ctx context.Context, id pgtype.UUID) (dbgen.WebhookDelivery, error)
	ResetDeliveryForReplay(ctx context.Context, id pgtype.UUID) (dbgen.WebhookDelivery, error)
	DeleteDlqByDelivery(ctx context.Context, deliveryID pgtype.UUID) error
	ListWebhookDeliveries(ctx context.Context, arg dbgen.ListWebhookDeliveriesParams) ([]dbgen.ListWebhookDeliveriesRow, error)
	CountWebhookDeliveries(ctx context.Context, arg dbgen.CountWebhookDeliveriesParams) (int64, error)

	GetDomainEvent(ctx context.Context, id pgtype.UUID) (dbgen.DomainEvent, error)
}

// QueriesStore adapts sqlc generated queries to the Store interface.
type QueriesStore struct {
	*dbgen.Queries
}

// NewStore returns a Store backed by sqlc queries.
func NewStore(q *dbgen.Queries) Store {
	if q == nil {
		return nil
	}
	return QueriesStore{Queries: q}
}

// The following methods provide explicit interface implementation wrappers.

func (s QueriesStore) CreateWebhookEndpoint(ctx context.Context, arg dbgen.CreateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return s.Queries.CreateWebhookEndpoint(ctx, arg)
}

func (s QueriesStore) UpdateWebhookEndpoint(ctx context.Context, arg dbgen.UpdateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return s.Queries.UpdateWebhookEndpoint(ctx, arg)
}

func (s QueriesStore) GetWebhookEndpoint(ctx context.Context, id pgtype.UUID) (dbgen.WebhookEndpoint, error) {
	return s.Queries.GetWebhookEndpoint(ctx, id)
}

func (s QueriesStore) ListWebhookEndpoints(ctx context.Context, arg dbgen.ListWebhookEndpointsParams) ([]dbgen.WebhookEndpoint, error) {
	return s.Queries.ListWebhookEndpoints(ctx, arg)
}

func (s QueriesStore) DeleteWebhookEndpoint(ctx context.Context, id pgtype.UUID) error {
	return s.Queries.DeleteWebhookEndpoint(ctx, id)
}

func (s QueriesStore) ListActiveEndpointsForTopic(ctx context.Context, topic string) ([]dbgen.WebhookEndpoint, error) {
	return s.Queries.ListActiveEndpointsForTopic(ctx, topic)
}

func (s QueriesStore) EnqueueDelivery(ctx context.Context, arg dbgen.EnqueueDeliveryParams) (dbgen.WebhookDelivery, error) {
	return s.Queries.EnqueueDelivery(ctx, arg)
}

func (s QueriesStore) DequeueDueDeliveries(ctx context.Context, limit int32) ([]dbgen.WebhookDelivery, error) {
	return s.Queries.DequeueDueDeliveries(ctx, limit)
}

func (s QueriesStore) MarkDelivering(ctx context.Context, id pgtype.UUID) error {
	return s.Queries.MarkDelivering(ctx, id)
}

func (s QueriesStore) MarkDelivered(ctx context.Context, arg dbgen.MarkDeliveredParams) error {
	return s.Queries.MarkDelivered(ctx, arg)
}

func (s QueriesStore) MarkFailedWithBackoff(ctx context.Context, arg dbgen.MarkFailedWithBackoffParams) error {
	return s.Queries.MarkFailedWithBackoff(ctx, arg)
}

func (s QueriesStore) MoveToDLQ(ctx context.Context, arg dbgen.MoveToDLQParams) error {
	return s.Queries.MoveToDLQ(ctx, arg)
}

func (s QueriesStore) InsertWebhookDlq(ctx context.Context, arg dbgen.InsertWebhookDlqParams) (dbgen.WebhookDlq, error) {
	return s.Queries.InsertWebhookDlq(ctx, arg)
}

func (s QueriesStore) GetDeliveryByID(ctx context.Context, id pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return s.Queries.GetDeliveryByID(ctx, id)
}

func (s QueriesStore) ResetDeliveryForReplay(ctx context.Context, id pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return s.Queries.ResetDeliveryForReplay(ctx, id)
}

func (s QueriesStore) DeleteDlqByDelivery(ctx context.Context, deliveryID pgtype.UUID) error {
	return s.Queries.DeleteDlqByDelivery(ctx, deliveryID)
}

func (s QueriesStore) ListWebhookDeliveries(ctx context.Context, arg dbgen.ListWebhookDeliveriesParams) ([]dbgen.ListWebhookDeliveriesRow, error) {
	return s.Queries.ListWebhookDeliveries(ctx, arg)
}

func (s QueriesStore) CountWebhookDeliveries(ctx context.Context, arg dbgen.CountWebhookDeliveriesParams) (int64, error) {
	return s.Queries.CountWebhookDeliveries(ctx, arg)
}

func (s QueriesStore) GetDomainEvent(ctx context.Context, id pgtype.UUID) (dbgen.DomainEvent, error) {
	return s.Queries.GetDomainEvent(ctx, id)
}
