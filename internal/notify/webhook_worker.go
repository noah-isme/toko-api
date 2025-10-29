package notify

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/lock"
)

// DeliveryWorker wraps webhook delivery execution with distributed locking.
type DeliveryWorker struct {
	Dispatcher *Dispatcher
	Locker     lock.Locker
	LockTTL    time.Duration
}

// Handle executes the delivery identified by payload.
func (w DeliveryWorker) Handle(ctx context.Context, payload []byte) error {
	if w.Dispatcher == nil {
		return errors.New("webhook worker: dispatcher not configured")
	}
	deliveryID := strings.TrimSpace(string(payload))
	if deliveryID == "" {
		return nil
	}
	ttl := w.LockTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	key := fmt.Sprintf("lock:delivery:%s", deliveryID)
	return w.Locker.WithLock(ctx, key, ttl, func(ctx context.Context) error {
		if w.Dispatcher.Store == nil {
			return errors.New("webhook worker: dispatcher store unavailable")
		}
		parsed, err := uuid.Parse(deliveryID)
		if err != nil {
			return fmt.Errorf("invalid delivery id: %w", err)
		}
		id := pgtype.UUID{Bytes: [16]byte(parsed), Valid: true}
		delivery, err := w.Dispatcher.Store.GetDeliveryByID(ctx, id)
		if err != nil {
			return err
		}
		if delivery.Status == dbgen.DeliveryStatusDELIVERED || delivery.Status == dbgen.DeliveryStatusDLQ {
			return nil
		}
		return w.Dispatcher.DeliverByID(ctx, deliveryID)
	})
}
