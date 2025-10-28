package shipping

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

var (
	// ErrShipmentAlreadyExists is returned when a shipment has been created previously.
	ErrShipmentAlreadyExists = errors.New("shipment already exists for order")
	// ErrOrderNotEligible is returned when the order cannot be transitioned into a shippable state.
	ErrOrderNotEligible = errors.New("order status does not allow creating a shipment")
	// ErrInvalidShipmentTransition is returned when a shipment status transition would break the state machine.
	ErrInvalidShipmentTransition = errors.New("invalid shipment status transition")
)

type queryProvider interface {
	GetOrderByID(ctx context.Context, id pgtype.UUID) (dbgen.Order, error)
	GetShipmentByOrder(ctx context.Context, orderID pgtype.UUID) (dbgen.Shipment, error)
	CreateShipment(ctx context.Context, arg dbgen.CreateShipmentParams) (dbgen.Shipment, error)
	UpdateOrderStatusIfAllowed(ctx context.Context, arg dbgen.UpdateOrderStatusIfAllowedParams) (pgtype.UUID, error)
	InsertShipmentEvent(ctx context.Context, arg dbgen.InsertShipmentEventParams) (dbgen.ShipmentEvent, error)
	UpdateShipmentStatus(ctx context.Context, arg dbgen.UpdateShipmentStatusParams) (pgtype.UUID, error)
	GetOrderStatus(ctx context.Context, id pgtype.UUID) (dbgen.OrderStatus, error)
	GetUserByID(ctx context.Context, id pgtype.UUID) (dbgen.GetUserByIDRow, error)
}

// Service coordinates shipment creation, tracking updates and notifications.
type Service struct {
	Q                      queryProvider
	Provider               Provider
	Mail                   common.EmailSender
	NotifyOnShipped        bool
	NotifyOnOutForDelivery bool
	NotifyOnDelivered      bool
	Events                 *events.Bus
}

// Create initialises a shipment for the provided order and records courier metadata.
func (s *Service) Create(ctx context.Context, orderID pgtype.UUID, courier, tracking string) (dbgen.Shipment, error) {
	if s.Q == nil {
		return dbgen.Shipment{}, errors.New("shipment queries not configured")
	}
	// Ensure the order exists and is in a state that can transition to PACKED.
	order, err := s.Q.GetOrderByID(ctx, orderID)
	if err != nil {
		return dbgen.Shipment{}, err
	}
	if order.Status != dbgen.OrderStatusPAID && order.Status != dbgen.OrderStatusPACKED {
		return dbgen.Shipment{}, ErrOrderNotEligible
	}
	if _, err := s.Q.GetShipmentByOrder(ctx, orderID); err == nil {
		return dbgen.Shipment{}, ErrShipmentAlreadyExists
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return dbgen.Shipment{}, err
	}
	shipment, err := s.Q.CreateShipment(ctx, dbgen.CreateShipmentParams{
		OrderID:        orderID,
		Courier:        optionalText(courier),
		TrackingNumber: optionalText(tracking),
	})
	if err != nil {
		return dbgen.Shipment{}, err
	}
	// Transition order into PACKED when shipment is created.
	_, updateErr := s.Q.UpdateOrderStatusIfAllowed(ctx, dbgen.UpdateOrderStatusIfAllowedParams{
		ID:     orderID,
		Status: dbgen.OrderStatusPACKED,
	})
	if updateErr != nil && !errors.Is(updateErr, pgx.ErrNoRows) {
		return shipment, updateErr
	}
	return shipment, nil
}

// AppendEvent records a tracking event, updates the shipment state machine and synchronises the order status.
func (s *Service) AppendEvent(ctx context.Context, orderID pgtype.UUID, status dbgen.ShipmentStatus, description, location *string, occurredAt *time.Time, payload []byte) (dbgen.ShipmentEvent, dbgen.Shipment, error) {
	if s.Q == nil {
		return dbgen.ShipmentEvent{}, dbgen.Shipment{}, errors.New("shipment queries not configured")
	}
	shipment, err := s.Q.GetShipmentByOrder(ctx, orderID)
	if err != nil {
		return dbgen.ShipmentEvent{}, dbgen.Shipment{}, err
	}
	current := shipment.Status
	if shipment.LastStatus.Valid {
		current = shipment.LastStatus.ShipmentStatus
	}
	if !allowedShipmentTransition(current, status) {
		return dbgen.ShipmentEvent{}, dbgen.Shipment{}, ErrInvalidShipmentTransition
	}
	event, err := s.Q.InsertShipmentEvent(ctx, dbgen.InsertShipmentEventParams{
		ShipmentID:  shipment.ID,
		Status:      status,
		Description: optionalNullableText(description),
		Location:    optionalNullableText(location),
		OccurredAt:  optionalTime(occurredAt),
		RawPayload:  payload,
	})
	if err != nil {
		return dbgen.ShipmentEvent{}, dbgen.Shipment{}, err
	}
	if _, err := s.Q.UpdateShipmentStatus(ctx, dbgen.UpdateShipmentStatusParams{ID: shipment.ID, Status: status}); err != nil {
		return event, shipment, err
	}
	shipment.Status = status
	shipment.LastStatus = dbgen.NullShipmentStatus{ShipmentStatus: status, Valid: true}
	if occurredAt != nil {
		shipment.LastEventAt = pgtype.Timestamptz{Time: *occurredAt, Valid: true}
	} else {
		shipment.LastEventAt = event.OccurredAt
	}
	if err := s.syncOrderStatus(ctx, orderID, status); err != nil {
		return event, shipment, err
	}
	s.notify(ctx, orderID, status)
	s.emit(ctx, orderID, shipment.ID, status, payload)
	return event, shipment, nil
}

func (s *Service) syncOrderStatus(ctx context.Context, orderID pgtype.UUID, status dbgen.ShipmentStatus) error {
	target, ok := shipmentToOrderStatus(status)
	if !ok {
		return nil
	}
	current, err := s.Q.GetOrderStatus(ctx, orderID)
	if err != nil {
		return err
	}
	if orderStatusRank(current) >= orderStatusRank(target) {
		return nil
	}
	_, err = s.Q.UpdateOrderStatusIfAllowed(ctx, dbgen.UpdateOrderStatusIfAllowedParams{ID: orderID, Status: target})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

func (s *Service) notify(ctx context.Context, orderID pgtype.UUID, status dbgen.ShipmentStatus) {
	if s.Mail == nil {
		return
	}
	switch status {
	case dbgen.ShipmentStatusSHIPPED:
		if !s.NotifyOnShipped {
			return
		}
	case dbgen.ShipmentStatusOUTFORDELIVERY:
		if !s.NotifyOnOutForDelivery {
			return
		}
	case dbgen.ShipmentStatusDELIVERED:
		if !s.NotifyOnDelivered {
			return
		}
	default:
		return
	}
	order, err := s.Q.GetOrderByID(ctx, orderID)
	if err != nil {
		return
	}
	user, err := s.Q.GetUserByID(ctx, order.UserID)
	if err != nil {
		return
	}
	subject, body := notificationContent(status)
	_ = s.Mail.Send(user.Email, subject, body)
}

func (s *Service) emit(ctx context.Context, orderID, shipmentID pgtype.UUID, status dbgen.ShipmentStatus, raw []byte) {
	if s.Events == nil {
		return
	}
	topic, ok := shipmentTopic(status)
	if !ok {
		return
	}
	data := map[string]any{
		"orderId":    uuidString(orderID),
		"shipmentId": uuidString(shipmentID),
		"status":     string(status),
	}
	if len(raw) > 0 {
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err == nil {
			data["payload"] = parsed
		}
	}
	if order, err := s.Q.GetOrderByID(ctx, orderID); err == nil {
		if user, err := s.Q.GetUserByID(ctx, order.UserID); err == nil && user.Email != "" {
			data["email"] = user.Email
		}
	}
	_, _ = s.Events.Emit(ctx, topic, shipmentID, data)
}

func shipmentTopic(status dbgen.ShipmentStatus) (string, bool) {
	switch status {
	case dbgen.ShipmentStatusSHIPPED:
		return events.TopicShipmentShipped, true
	case dbgen.ShipmentStatusOUTFORDELIVERY:
		return events.TopicShipmentOutForDelivery, true
	case dbgen.ShipmentStatusDELIVERED:
		return events.TopicShipmentDelivered, true
	default:
		return "", false
	}
}

func allowedShipmentTransition(current, next dbgen.ShipmentStatus) bool {
	if current == next {
		return true
	}
	switch current {
	case dbgen.ShipmentStatusPENDING:
		return next == dbgen.ShipmentStatusSHIPPED
	case dbgen.ShipmentStatusSHIPPED:
		return next == dbgen.ShipmentStatusOUTFORDELIVERY || next == dbgen.ShipmentStatusDELIVERED
	case dbgen.ShipmentStatusOUTFORDELIVERY:
		return next == dbgen.ShipmentStatusDELIVERED
	default:
		return false
	}
}

func shipmentToOrderStatus(status dbgen.ShipmentStatus) (dbgen.OrderStatus, bool) {
	switch status {
	case dbgen.ShipmentStatusSHIPPED:
		return dbgen.OrderStatusSHIPPED, true
	case dbgen.ShipmentStatusOUTFORDELIVERY:
		return dbgen.OrderStatusOUTFORDELIVERY, true
	case dbgen.ShipmentStatusDELIVERED:
		return dbgen.OrderStatusDELIVERED, true
	}
	return "", false
}

func orderStatusRank(status dbgen.OrderStatus) int {
	switch status {
	case dbgen.OrderStatusPENDINGPAYMENT:
		return 0
	case dbgen.OrderStatusPAID:
		return 1
	case dbgen.OrderStatusPACKED:
		return 2
	case dbgen.OrderStatusSHIPPED:
		return 3
	case dbgen.OrderStatusOUTFORDELIVERY:
		return 4
	case dbgen.OrderStatusDELIVERED:
		return 5
	case dbgen.OrderStatusCANCELED:
		return -1
	default:
		return -2
	}
}

func notificationContent(status dbgen.ShipmentStatus) (string, string) {
	switch status {
	case dbgen.ShipmentStatusSHIPPED:
		return "Pesanan dikirim", "Pesanan Anda telah dikirim."
	case dbgen.ShipmentStatusOUTFORDELIVERY:
		return "Sedang dikirim", "Pesanan Anda sedang dalam perjalanan."
	case dbgen.ShipmentStatusDELIVERED:
		return "Terkirim", "Pesanan Anda telah diterima."
	default:
		return "", ""
	}
}

func optionalText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func optionalNullableText(value *string) pgtype.Text {
	if value == nil || *value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}
