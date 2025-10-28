package shipping_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

func TestNotifyOnDelivered(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	userID := uuid.New()

	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(userID), Status: dbgen.OrderStatusPAID}, "buyer@example.com")

	mailer := &recordingMailer{}
	svc := &shipping.Service{
		Q:                 queries,
		Mail:              mailer,
		NotifyOnDelivered: true,
	}

	_, err := svc.Create(ctx, toPGUUID(orderID), "jne", "TRACK123")
	require.NoError(t, err)

	_, _, err = svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusSHIPPED, nil, nil, nil, []byte(`{}`))
	require.NoError(t, err)
	_, _, err = svc.AppendEvent(ctx, toPGUUID(orderID), dbgen.ShipmentStatusDELIVERED, nil, nil, nil, []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Terkirim"}, mailer.Subjects())
}
