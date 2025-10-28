package shipping_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

func TestStatusTransitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	userID := uuid.New()

	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(userID), Status: dbgen.OrderStatusPAID}, "buyer@example.com")

	mailer := &recordingMailer{}
	svc := &shipping.Service{
		Q:                      queries,
		Mail:                   mailer,
		NotifyOnShipped:        true,
		NotifyOnOutForDelivery: true,
		NotifyOnDelivered:      true,
	}

	shipment, err := svc.Create(ctx, toPGUUID(orderID), "jne", "TRACK123")
	require.NoError(t, err)
	require.Equal(t, dbgen.ShipmentStatusPENDING, shipment.Status)
	require.Equal(t, dbgen.OrderStatusPACKED, queries.orders[orderID.String()].Status)

	for _, status := range []dbgen.ShipmentStatus{
		dbgen.ShipmentStatusSHIPPED,
		dbgen.ShipmentStatusOUTFORDELIVERY,
		dbgen.ShipmentStatusDELIVERED,
	} {
		_, _, err := svc.AppendEvent(ctx, toPGUUID(orderID), status, nil, nil, nil, []byte(`{"event":"ok"}`))
		require.NoError(t, err)
	}

	require.Len(t, queries.events, 3)
	require.Equal(t, dbgen.OrderStatusDELIVERED, queries.orders[orderID.String()].Status)
	require.Equal(t, dbgen.ShipmentStatusDELIVERED, queries.shipments[orderID.String()].Status)
	require.ElementsMatch(t, []string{"Pesanan dikirim", "Sedang dikirim", "Terkirim"}, mailer.Subjects())

	_, _, err = svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusOUTFORDELIVERY, nil, nil, nil, []byte(`{}`))
	require.ErrorIs(t, err, shipping.ErrInvalidShipmentTransition)
}

func TestCreateRejectsInvalidStatus(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), Status: dbgen.OrderStatusPENDINGPAYMENT}, "")

	svc := &shipping.Service{Q: queries}
	_, err := svc.Create(ctx, toPGUUID(orderID), "jne", "TRACK123")
	require.ErrorIs(t, err, shipping.ErrOrderNotEligible)
}
