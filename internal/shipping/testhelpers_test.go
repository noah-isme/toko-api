package shipping_test

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type mockQueries struct {
	mu            sync.Mutex
	orders        map[string]*dbgen.Order
	shipments     map[string]*dbgen.Shipment
	shipmentsByID map[string]*dbgen.Shipment
	users         map[string]dbgen.GetUserByIDRow
	events        []dbgen.ShipmentEvent
}

func newMockQueries() *mockQueries {
	return &mockQueries{
		orders:        make(map[string]*dbgen.Order),
		shipments:     make(map[string]*dbgen.Shipment),
		shipmentsByID: make(map[string]*dbgen.Shipment),
		users:         make(map[string]dbgen.GetUserByIDRow),
	}
}

func (m *mockQueries) addOrder(order dbgen.Order, email string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uuidFromPG(order.ID).String()
	copyOrder := order
	m.orders[key] = &copyOrder
	if email != "" {
		m.users[uuidFromPG(order.UserID).String()] = dbgen.GetUserByIDRow{ID: order.UserID, Email: email}
	}
}

func (m *mockQueries) storeShipment(shipment dbgen.Shipment) {
	key := uuidFromPG(shipment.OrderID).String()
	idKey := uuidFromPG(shipment.ID).String()
	copyShipment := shipment
	m.shipments[key] = &copyShipment
	m.shipmentsByID[idKey] = &copyShipment
}

func (m *mockQueries) GetOrderByID(ctx context.Context, id pgtype.UUID) (dbgen.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if order, ok := m.orders[uuidFromPG(id).String()]; ok {
		copyOrder := *order
		return copyOrder, nil
	}
	return dbgen.Order{}, pgx.ErrNoRows
}

func (m *mockQueries) GetShipmentByOrder(ctx context.Context, orderID pgtype.UUID) (dbgen.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if shipment, ok := m.shipments[uuidFromPG(orderID).String()]; ok {
		copyShipment := *shipment
		return copyShipment, nil
	}
	return dbgen.Shipment{}, pgx.ErrNoRows
}

func (m *mockQueries) CreateShipment(ctx context.Context, arg dbgen.CreateShipmentParams) (dbgen.Shipment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uuidFromPG(arg.OrderID).String()
	if _, exists := m.shipments[key]; exists {
		return dbgen.Shipment{}, errors.New("shipment exists")
	}
	shipment := dbgen.Shipment{
		ID:             toPGUUID(uuid.New()),
		OrderID:        arg.OrderID,
		Status:         dbgen.ShipmentStatusPENDING,
		Courier:        arg.Courier,
		TrackingNumber: arg.TrackingNumber,
		History:        []byte("[]"),
		LastStatus:     dbgen.NullShipmentStatus{ShipmentStatus: dbgen.ShipmentStatusPENDING, Valid: true},
		LastEventAt:    pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	m.storeShipment(shipment)
	return shipment, nil
}

func (m *mockQueries) UpdateOrderStatusIfAllowed(ctx context.Context, arg dbgen.UpdateOrderStatusIfAllowedParams) (pgtype.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := uuidFromPG(arg.ID).String()
	order, ok := m.orders[key]
	if !ok {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	if !allowedOrderTransition(order.Status, arg.Status) {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	order.Status = arg.Status
	return arg.ID, nil
}

func (m *mockQueries) InsertShipmentEvent(ctx context.Context, arg dbgen.InsertShipmentEventParams) (dbgen.ShipmentEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	shipment, ok := m.shipmentsByID[uuidFromPG(arg.ShipmentID).String()]
	if !ok {
		return dbgen.ShipmentEvent{}, pgx.ErrNoRows
	}
	occurred := arg.OccurredAt
	if !occurred.Valid {
		occurred = pgtype.Timestamptz{Time: time.Now(), Valid: true}
	}
	event := dbgen.ShipmentEvent{
		ID:          toPGUUID(uuid.New()),
		ShipmentID:  arg.ShipmentID,
		Status:      arg.Status,
		Description: arg.Description,
		Location:    arg.Location,
		OccurredAt:  occurred,
		RawPayload:  arg.RawPayload,
		CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
	m.events = append(m.events, event)
	shipment.LastEventAt = occurred
	return event, nil
}

func (m *mockQueries) UpdateShipmentStatus(ctx context.Context, arg dbgen.UpdateShipmentStatusParams) (pgtype.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	shipment, ok := m.shipmentsByID[uuidFromPG(arg.ID).String()]
	if !ok {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	shipment.Status = arg.Status
	shipment.LastStatus = dbgen.NullShipmentStatus{ShipmentStatus: arg.Status, Valid: true}
	orderKey := uuidFromPG(shipment.OrderID).String()
	m.shipments[orderKey] = shipment
	m.shipmentsByID[uuidFromPG(shipment.ID).String()] = shipment
	return arg.ID, nil
}

func (m *mockQueries) GetOrderStatus(ctx context.Context, id pgtype.UUID) (dbgen.OrderStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if order, ok := m.orders[uuidFromPG(id).String()]; ok {
		return order.Status, nil
	}
	return "", pgx.ErrNoRows
}

func (m *mockQueries) GetUserByID(ctx context.Context, id pgtype.UUID) (dbgen.GetUserByIDRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if user, ok := m.users[uuidFromPG(id).String()]; ok {
		return user, nil
	}
	return dbgen.GetUserByIDRow{}, pgx.ErrNoRows
}

func (m *mockQueries) GetOrderByIDForUser(ctx context.Context, arg dbgen.GetOrderByIDForUserParams) (dbgen.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if order, ok := m.orders[uuidFromPG(arg.ID).String()]; ok && order.UserID == arg.UserID {
		copyOrder := *order
		return copyOrder, nil
	}
	return dbgen.Order{}, pgx.ErrNoRows
}

func (m *mockQueries) ListShipmentEvents(ctx context.Context, shipmentID pgtype.UUID) ([]dbgen.ShipmentEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]dbgen.ShipmentEvent, 0)
	for _, ev := range m.events {
		if uuidFromPG(ev.ShipmentID) == uuidFromPG(shipmentID) {
			result = append(result, ev)
		}
	}
	return result, nil
}

func allowedOrderTransition(current, next dbgen.OrderStatus) bool {
	switch current {
	case dbgen.OrderStatusPENDINGPAYMENT:
		return next == dbgen.OrderStatusPAID || next == dbgen.OrderStatusCANCELED
	case dbgen.OrderStatusPAID:
		return next == dbgen.OrderStatusPACKED || next == dbgen.OrderStatusCANCELED
	case dbgen.OrderStatusPACKED:
		return next == dbgen.OrderStatusSHIPPED
	case dbgen.OrderStatusSHIPPED:
		return next == dbgen.OrderStatusOUTFORDELIVERY
	case dbgen.OrderStatusOUTFORDELIVERY:
		return next == dbgen.OrderStatusDELIVERED
	default:
		return false
	}
}

func uuidFromPG(id pgtype.UUID) uuid.UUID {
	return uuid.UUID(id.Bytes)
}

func toPGUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

type recordingMailer struct {
	mu     sync.Mutex
	sent   []string
	bodies []string
}

func (r *recordingMailer) Send(to, subject, body string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, subject)
	r.bodies = append(r.bodies, body)
	return nil
}

func (r *recordingMailer) Subjects() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.sent))
	copy(out, r.sent)
	return out
}
