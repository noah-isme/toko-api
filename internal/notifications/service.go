package notifications

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Store captures the persistence operations the notifications module relies on.
// It is satisfied by *dbgen.Queries and kept narrow so tests can stub it.
type Store interface {
	CreateNotification(ctx context.Context, arg dbgen.CreateNotificationParams) (dbgen.Notification, error)
	ListNotifications(ctx context.Context, arg dbgen.ListNotificationsParams) ([]dbgen.Notification, error)
	CountNotifications(ctx context.Context, arg dbgen.CountNotificationsParams) (int64, error)
	CountUnreadNotifications(ctx context.Context, arg dbgen.CountUnreadNotificationsParams) (int64, error)
	MarkNotificationRead(ctx context.Context, arg dbgen.MarkNotificationReadParams) (pgtype.UUID, error)
	MarkAllNotificationsRead(ctx context.Context, arg dbgen.MarkAllNotificationsReadParams) error
}

// Service exposes in-app notification operations backed by a Store.
type Service struct {
	Q Store
}

// Create persists a single in-app notification for a user. The data payload is
// stored verbatim as JSON; callers pass nil or valid JSON bytes.
func (s *Service) Create(ctx context.Context, userID, tenantID pgtype.UUID, kind, title, body string, data []byte) (dbgen.Notification, error) {
	if len(data) == 0 {
		data = []byte("{}")
	}
	return s.Q.CreateNotification(ctx, dbgen.CreateNotificationParams{
		UserID:   userID,
		TenantID: tenantID,
		Type:     kind,
		Title:    title,
		Body:     body,
		Data:     data,
	})
}

// List returns a page of notifications for a user together with the total count.
func (s *Service) List(ctx context.Context, userID, tenantID pgtype.UUID, page, limit int32) ([]dbgen.Notification, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit
	items, err := s.Q.ListNotifications(ctx, dbgen.ListNotificationsParams{
		UserID:   userID,
		TenantID: tenantID,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.Q.CountNotifications(ctx, dbgen.CountNotificationsParams{
		UserID:   userID,
		TenantID: tenantID,
	})
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// UnreadCount returns the number of unread notifications for a user.
func (s *Service) UnreadCount(ctx context.Context, userID, tenantID pgtype.UUID) (int64, error) {
	return s.Q.CountUnreadNotifications(ctx, dbgen.CountUnreadNotificationsParams{
		UserID:   userID,
		TenantID: tenantID,
	})
}

// MarkRead marks a single notification read. It reports whether a row was
// updated (false when the notification does not exist or was already read).
func (s *Service) MarkRead(ctx context.Context, id, userID, tenantID pgtype.UUID) (bool, error) {
	_, err := s.Q.MarkNotificationRead(ctx, dbgen.MarkNotificationReadParams{
		ID:       id,
		UserID:   userID,
		TenantID: tenantID,
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkAllRead marks every unread notification for a user as read.
func (s *Service) MarkAllRead(ctx context.Context, userID, tenantID pgtype.UUID) error {
	return s.Q.MarkAllNotificationsRead(ctx, dbgen.MarkAllNotificationsReadParams{
		UserID:   userID,
		TenantID: tenantID,
	})
}
