package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

var errNotImplemented = errors.New("not implemented")

type fakeQueries struct {
	mu              sync.Mutex
	usersByEmail    map[string]dbgen.User
	usersByID       map[string]dbgen.User
	sessionsByToken map[string]dbgen.Session
	sessionsByID    map[string]dbgen.Session
	resetsByToken   map[string]dbgen.PasswordReset
	resetsByID      map[string]dbgen.PasswordReset
}

func newFakeQueries() *fakeQueries {
	return &fakeQueries{
		usersByEmail:    make(map[string]dbgen.User),
		usersByID:       make(map[string]dbgen.User),
		sessionsByToken: make(map[string]dbgen.Session),
		sessionsByID:    make(map[string]dbgen.Session),
		resetsByToken:   make(map[string]dbgen.PasswordReset),
		resetsByID:      make(map[string]dbgen.PasswordReset),
	}
}

func (f *fakeQueries) CountAddressesByUser(context.Context, pgtype.UUID) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) CountWebhookDeliveries(context.Context, dbgen.CountWebhookDeliveriesParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) ListBrands(context.Context) ([]dbgen.ListBrandsRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) GetBrandByID(context.Context, pgtype.UUID) (dbgen.GetBrandByIDRow, error) {
	return dbgen.GetBrandByIDRow{}, errNotImplemented
}

func (f *fakeQueries) GetBrandBySlug(context.Context, string) (dbgen.GetBrandBySlugRow, error) {
	return dbgen.GetBrandBySlugRow{}, errNotImplemented
}

func (f *fakeQueries) ListCategories(context.Context) ([]dbgen.ListCategoriesRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) GetCategoryByID(context.Context, pgtype.UUID) (dbgen.GetCategoryByIDRow, error) {
	return dbgen.GetCategoryByIDRow{}, errNotImplemented
}

func (f *fakeQueries) GetCategoryBySlug(context.Context, string) (dbgen.GetCategoryBySlugRow, error) {
	return dbgen.GetCategoryBySlugRow{}, errNotImplemented
}

func (f *fakeQueries) CountProductsPublic(context.Context, dbgen.CountProductsPublicParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) ListProductsPublic(context.Context, dbgen.ListProductsPublicParams) ([]dbgen.ListProductsPublicRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) GetProductBySlug(context.Context, string) (dbgen.GetProductBySlugRow, error) {
	return dbgen.GetProductBySlugRow{}, errNotImplemented
}

func (f *fakeQueries) ListVariantsByProduct(context.Context, pgtype.UUID) ([]dbgen.ProductVariant, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListImagesByProduct(context.Context, pgtype.UUID) ([]dbgen.ProductImage, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListSpecsByProduct(context.Context, pgtype.UUID) ([]dbgen.ProductSpec, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListRelatedByCategory(context.Context, dbgen.ListRelatedByCategoryParams) ([]dbgen.ListRelatedByCategoryRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) CreateAddress(context.Context, dbgen.CreateAddressParams) (dbgen.Address, error) {
	return dbgen.Address{}, errNotImplemented
}

func (f *fakeQueries) CreateWebhookEndpoint(context.Context, dbgen.CreateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, errNotImplemented
}

func (f *fakeQueries) DeleteAddress(context.Context, dbgen.DeleteAddressParams) error {
	return errNotImplemented
}

func (f *fakeQueries) DeleteDlqByDelivery(context.Context, pgtype.UUID) error {
	return errNotImplemented
}

func (f *fakeQueries) GetAddressByID(context.Context, dbgen.GetAddressByIDParams) (dbgen.Address, error) {
	return dbgen.Address{}, errNotImplemented
}

func (f *fakeQueries) ListAddressesByUser(context.Context, dbgen.ListAddressesByUserParams) ([]dbgen.Address, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) DeleteWebhookEndpoint(context.Context, pgtype.UUID) error {
	return errNotImplemented
}

func (f *fakeQueries) ListAuditLogs(context.Context, dbgen.ListAuditLogsParams) ([]dbgen.AuditLog, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) InsertAuditLog(context.Context, dbgen.InsertAuditLogParams) (dbgen.InsertAuditLogRow, error) {
	return dbgen.InsertAuditLogRow{}, nil
}

func (f *fakeQueries) DequeueDueDeliveries(context.Context, int32) ([]dbgen.WebhookDelivery, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) UnsetDefaultAddresses(context.Context, dbgen.UnsetDefaultAddressesParams) error {
	return errNotImplemented
}

func (f *fakeQueries) UpdateAddress(context.Context, dbgen.UpdateAddressParams) (dbgen.Address, error) {
	return dbgen.Address{}, errNotImplemented
}

func (f *fakeQueries) UpdateUserProfile(context.Context, dbgen.UpdateUserProfileParams) (dbgen.UpdateUserProfileRow, error) {
	return dbgen.UpdateUserProfileRow{}, errNotImplemented
}

func (f *fakeQueries) CreateUser(ctx context.Context, arg dbgen.CreateUserParams) (dbgen.CreateUserRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	id := uuid.New()
	pgID, _ := pgUUIDFromString(id.String())
	now := time.Now()
	user := dbgen.User{
		ID:           pgID,
		Name:         arg.Name,
		Email:        arg.Email,
		PasswordHash: arg.PasswordHash,
		Roles:        []string{"user"},
		CreatedAt:    pgTimestamp(now),
		UpdatedAt:    pgTimestamp(now),
	}
	f.usersByEmail[strings.ToLower(arg.Email)] = user
	f.usersByID[id.String()] = user

	return dbgen.CreateUserRow{
		ID:        pgID,
		Name:      arg.Name,
		Email:     arg.Email,
		Roles:     user.Roles,
		CreatedAt: pgTimestamp(now),
		UpdatedAt: pgTimestamp(now),
	}, nil
}
func (f *fakeQueries) GetUserByEmail(ctx context.Context, email string) (dbgen.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.usersByEmail[strings.ToLower(email)]
	if !ok {
		return dbgen.User{}, fmt.Errorf("user not found")
	}
	return user, nil
}

func (f *fakeQueries) GetUserByID(ctx context.Context, id pgtype.UUID) (dbgen.GetUserByIDRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(id)
	user, ok := f.usersByID[key]
	if !ok {
		return dbgen.GetUserByIDRow{}, fmt.Errorf("user not found")
	}
	return dbgen.GetUserByIDRow{
		ID:        user.ID,
		Name:      user.Name,
		Email:     user.Email,
		Roles:     user.Roles,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}, nil
}

func (f *fakeQueries) GetOrderByID(context.Context, pgtype.UUID) (dbgen.Order, error) {
	return dbgen.Order{}, errNotImplemented
}

func (f *fakeQueries) GetDeliveryByID(context.Context, pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, errNotImplemented
}

func (f *fakeQueries) GetDomainEvent(context.Context, pgtype.UUID) (dbgen.DomainEvent, error) {
	return dbgen.DomainEvent{}, errNotImplemented
}

func (f *fakeQueries) UpdateUserPassword(ctx context.Context, arg dbgen.UpdateUserPasswordParams) (dbgen.UpdateUserPasswordRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(arg.ID)
	user, ok := f.usersByID[key]
	if !ok {
		return dbgen.UpdateUserPasswordRow{}, fmt.Errorf("user not found")
	}
	user.PasswordHash = arg.PasswordHash
	user.UpdatedAt = pgTimestamp(time.Now())
	f.usersByID[key] = user
	f.usersByEmail[strings.ToLower(user.Email)] = user
	return dbgen.UpdateUserPasswordRow{
		ID:        user.ID,
		Name:      user.Name,
		Email:     user.Email,
		Roles:     user.Roles,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}, nil
}

func (f *fakeQueries) CreateSession(ctx context.Context, arg dbgen.CreateSessionParams) (dbgen.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	pgID, _ := pgUUIDFromString(id.String())
	session := dbgen.Session{
		ID:           pgID,
		UserID:       arg.UserID,
		RefreshToken: arg.RefreshToken,
		UserAgent:    arg.UserAgent,
		Ip:           arg.Ip,
		ExpiresAt:    arg.ExpiresAt,
		CreatedAt:    pgTimestamp(time.Now()),
	}
	f.sessionsByToken[arg.RefreshToken] = session
	f.sessionsByID[id.String()] = session
	return session, nil
}

func (f *fakeQueries) GetSessionByToken(ctx context.Context, token string) (dbgen.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessionsByToken[token]
	if !ok {
		return dbgen.Session{}, fmt.Errorf("session not found")
	}
	return session, nil
}

func (f *fakeQueries) RotateSessionToken(ctx context.Context, arg dbgen.RotateSessionTokenParams) (dbgen.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(arg.ID)
	session, ok := f.sessionsByID[key]
	if !ok {
		return dbgen.Session{}, fmt.Errorf("session not found")
	}
	delete(f.sessionsByToken, session.RefreshToken)
	session.RefreshToken = arg.RefreshToken
	session.ExpiresAt = arg.ExpiresAt
	f.sessionsByID[key] = session
	f.sessionsByToken[arg.RefreshToken] = session
	return session, nil
}

func (f *fakeQueries) DeleteSessionByToken(ctx context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessionsByToken[token]
	if !ok {
		return nil
	}
	key := uuidString(session.ID)
	delete(f.sessionsByToken, token)
	delete(f.sessionsByID, key)
	return nil
}

func (f *fakeQueries) DeleteSessionsByUser(ctx context.Context, userID pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(userID)
	for token, session := range f.sessionsByToken {
		if uuidString(session.UserID) == key {
			delete(f.sessionsByToken, token)
			delete(f.sessionsByID, uuidString(session.ID))
		}
	}
	return nil
}

func (f *fakeQueries) CreatePasswordReset(ctx context.Context, arg dbgen.CreatePasswordResetParams) (dbgen.PasswordReset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := uuid.New()
	pgID, _ := pgUUIDFromString(id.String())
	reset := dbgen.PasswordReset{
		ID:        pgID,
		UserID:    arg.UserID,
		Token:     arg.Token,
		ExpiresAt: arg.ExpiresAt,
		CreatedAt: pgTimestamp(time.Now()),
	}
	f.resetsByToken[arg.Token] = reset
	f.resetsByID[id.String()] = reset
	return reset, nil
}

func (f *fakeQueries) GetPasswordResetByToken(ctx context.Context, token string) (dbgen.PasswordReset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	reset, ok := f.resetsByToken[token]
	if !ok {
		return dbgen.PasswordReset{}, fmt.Errorf("reset not found")
	}
	return reset, nil
}

func (f *fakeQueries) UsePasswordReset(ctx context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	reset, ok := f.resetsByToken[token]
	if !ok {
		return fmt.Errorf("reset not found")
	}
	reset.UsedAt = pgTimestamp(time.Now())
	f.resetsByToken[token] = reset
	f.resetsByID[uuidString(reset.ID)] = reset
	return nil
}

func (f *fakeQueries) DeletePasswordReset(ctx context.Context, id pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(id)
	reset, ok := f.resetsByID[key]
	if !ok {
		return nil
	}
	delete(f.resetsByID, key)
	delete(f.resetsByToken, reset.Token)
	return nil
}

func (f *fakeQueries) DeletePasswordResetsByUser(ctx context.Context, userID pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(userID)
	for token, reset := range f.resetsByToken {
		if uuidString(reset.UserID) == key {
			delete(f.resetsByToken, token)
			delete(f.resetsByID, uuidString(reset.ID))
		}
	}
	return nil
}

func (f *fakeQueries) MarkPasswordResetUsed(ctx context.Context, id pgtype.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := uuidString(id)
	reset, ok := f.resetsByID[key]
	if !ok {
		return fmt.Errorf("reset not found")
	}
	reset.UsedAt = pgTimestamp(time.Now())
	f.resetsByID[key] = reset
	f.resetsByToken[reset.Token] = reset
	return nil
}

func (f *fakeQueries) CountOrdersForUser(context.Context, pgtype.UUID) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) CreateCart(context.Context, dbgen.CreateCartParams) (dbgen.Cart, error) {
	return dbgen.Cart{}, errNotImplemented
}

func (f *fakeQueries) CreateCartItem(context.Context, dbgen.CreateCartItemParams) (dbgen.CartItem, error) {
	return dbgen.CartItem{}, errNotImplemented
}

func (f *fakeQueries) CountVoucherUsageByUser(context.Context, dbgen.CountVoucherUsageByUserParams) (int64, error) {
	return 0, errNotImplemented
}

func (f *fakeQueries) CreateOrder(context.Context, dbgen.CreateOrderParams) (dbgen.Order, error) {
	return dbgen.Order{}, errNotImplemented
}

func (f *fakeQueries) CreateOrderItem(context.Context, dbgen.CreateOrderItemParams) error {
	return errNotImplemented
}

func (f *fakeQueries) CreatePayment(context.Context, dbgen.CreatePaymentParams) (dbgen.Payment, error) {
	return dbgen.Payment{}, errNotImplemented
}

func (f *fakeQueries) GetLatestPaymentByOrder(context.Context, pgtype.UUID) (dbgen.Payment, error) {
	return dbgen.Payment{}, errNotImplemented
}

func (f *fakeQueries) UpdatePaymentStatus(context.Context, dbgen.UpdatePaymentStatusParams) error {
	return errNotImplemented
}

func (f *fakeQueries) InsertPaymentEvent(context.Context, dbgen.InsertPaymentEventParams) error {
	return errNotImplemented
}

func (f *fakeQueries) ListOrderItemsForStock(context.Context, pgtype.UUID) ([]dbgen.ListOrderItemsForStockRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) DecrementVariantStock(context.Context, dbgen.DecrementVariantStockParams) error {
	return errNotImplemented
}

func (f *fakeQueries) IncrementVoucherUsageByCode(context.Context, string) error {
	return errNotImplemented
}

func (f *fakeQueries) DeleteCartItem(context.Context, dbgen.DeleteCartItemParams) error {
	return errNotImplemented
}

func (f *fakeQueries) FindCartItemByProductVariant(context.Context, dbgen.FindCartItemByProductVariantParams) (dbgen.CartItem, error) {
	return dbgen.CartItem{}, errNotImplemented
}

func (f *fakeQueries) GetActiveCartByAnon(context.Context, pgtype.Text) (dbgen.Cart, error) {
	return dbgen.Cart{}, errNotImplemented
}

func (f *fakeQueries) GetActiveCartByUser(context.Context, pgtype.UUID) (dbgen.Cart, error) {
	return dbgen.Cart{}, errNotImplemented
}

func (f *fakeQueries) GetCartByID(context.Context, pgtype.UUID) (dbgen.Cart, error) {
	return dbgen.Cart{}, errNotImplemented
}

func (f *fakeQueries) GetCartItemByID(context.Context, pgtype.UUID) (dbgen.CartItem, error) {
	return dbgen.CartItem{}, errNotImplemented
}

func (f *fakeQueries) GetOrderByIDForUser(context.Context, dbgen.GetOrderByIDForUserParams) (dbgen.Order, error) {
	return dbgen.Order{}, errNotImplemented
}

func (f *fakeQueries) GetProductForCart(context.Context, pgtype.UUID) (dbgen.GetProductForCartRow, error) {
	return dbgen.GetProductForCartRow{}, errNotImplemented
}

func (f *fakeQueries) GetVariantForCart(context.Context, pgtype.UUID) (dbgen.GetVariantForCartRow, error) {
	return dbgen.GetVariantForCartRow{}, errNotImplemented
}

func (f *fakeQueries) GetVoucherByCode(context.Context, string) (dbgen.Voucher, error) {
	return dbgen.Voucher{}, errNotImplemented
}

func (f *fakeQueries) CreateVoucher(context.Context, dbgen.CreateVoucherParams) (dbgen.Voucher, error) {
	return dbgen.Voucher{}, errNotImplemented
}

func (f *fakeQueries) UpdateVoucher(context.Context, dbgen.UpdateVoucherParams) (dbgen.Voucher, error) {
	return dbgen.Voucher{}, errNotImplemented
}

func (f *fakeQueries) GetVoucherByCodeForUpdate(context.Context, string) (dbgen.Voucher, error) {
	return dbgen.Voucher{}, errNotImplemented
}

func (f *fakeQueries) GetVoucherUsageByOrder(context.Context, dbgen.GetVoucherUsageByOrderParams) (dbgen.VoucherUsage, error) {
	return dbgen.VoucherUsage{}, errNotImplemented
}

func (f *fakeQueries) InsertVoucherUsage(context.Context, dbgen.InsertVoucherUsageParams) error {
	return errNotImplemented
}

func (f *fakeQueries) IncreaseVoucherUsedCount(context.Context, pgtype.UUID) error {
	return nil
}

func (f *fakeQueries) ListCartItems(context.Context, pgtype.UUID) ([]dbgen.CartItem, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListOrderItemsByOrder(context.Context, pgtype.UUID) ([]dbgen.OrderItem, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListOrdersForUser(context.Context, dbgen.ListOrdersForUserParams) ([]dbgen.Order, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) TouchCart(context.Context, dbgen.TouchCartParams) error {
	return errNotImplemented
}

func (f *fakeQueries) TransferCartToUser(context.Context, dbgen.TransferCartToUserParams) error {
	return errNotImplemented
}

func (f *fakeQueries) UpdateCartItemQty(context.Context, dbgen.UpdateCartItemQtyParams) (dbgen.CartItem, error) {
	return dbgen.CartItem{}, errNotImplemented
}

func (f *fakeQueries) UpdateCartVoucher(context.Context, dbgen.UpdateCartVoucherParams) error {
	return errNotImplemented
}

func (f *fakeQueries) UpdateOrderStatus(context.Context, dbgen.UpdateOrderStatusParams) error {
	return errNotImplemented
}

func (f *fakeQueries) CreateShipment(context.Context, dbgen.CreateShipmentParams) (dbgen.Shipment, error) {
	return dbgen.Shipment{}, errNotImplemented
}

func (f *fakeQueries) GetShipmentByOrder(context.Context, pgtype.UUID) (dbgen.Shipment, error) {
	return dbgen.Shipment{}, errNotImplemented
}

func (f *fakeQueries) InsertShipmentEvent(context.Context, dbgen.InsertShipmentEventParams) (dbgen.ShipmentEvent, error) {
	return dbgen.ShipmentEvent{}, errNotImplemented
}

func (f *fakeQueries) UpdateShipmentStatus(context.Context, dbgen.UpdateShipmentStatusParams) (pgtype.UUID, error) {
	return pgtype.UUID{}, errNotImplemented
}

func (f *fakeQueries) ListShipmentEvents(context.Context, pgtype.UUID) ([]dbgen.ShipmentEvent, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) GetOrderStatus(context.Context, pgtype.UUID) (dbgen.OrderStatus, error) {
	return "", errNotImplemented
}

func (f *fakeQueries) UpdateOrderStatusIfAllowed(context.Context, dbgen.UpdateOrderStatusIfAllowedParams) (pgtype.UUID, error) {
	return pgtype.UUID{}, errNotImplemented
}

func (f *fakeQueries) GetSalesDailyRange(context.Context, dbgen.GetSalesDailyRangeParams) ([]dbgen.GetSalesDailyRangeRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) GetTopProducts(context.Context, dbgen.GetTopProductsParams) ([]dbgen.MvTopProduct, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) RefreshSalesDaily(context.Context) error {
	return errNotImplemented
}

func (f *fakeQueries) RefreshTopProducts(context.Context) error {
	return errNotImplemented
}

func (f *fakeQueries) InsertDomainEvent(context.Context, dbgen.InsertDomainEventParams) (dbgen.DomainEvent, error) {
	return dbgen.DomainEvent{}, errNotImplemented
}

func (f *fakeQueries) InsertWebhookDlq(context.Context, dbgen.InsertWebhookDlqParams) (dbgen.WebhookDlq, error) {
	return dbgen.WebhookDlq{}, errNotImplemented
}

func (f *fakeQueries) ListActiveEndpointsForTopic(context.Context, string) ([]dbgen.WebhookEndpoint, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListDomainEventsByTopic(context.Context, dbgen.ListDomainEventsByTopicParams) ([]dbgen.DomainEvent, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) ListWebhookEndpoints(context.Context, dbgen.ListWebhookEndpointsParams) ([]dbgen.WebhookEndpoint, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) UpdateWebhookEndpoint(context.Context, dbgen.UpdateWebhookEndpointParams) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, errNotImplemented
}

func (f *fakeQueries) ListWebhookDeliveries(context.Context, dbgen.ListWebhookDeliveriesParams) ([]dbgen.ListWebhookDeliveriesRow, error) {
	return nil, errNotImplemented
}

func (f *fakeQueries) MarkDelivering(context.Context, pgtype.UUID) error {
	return errNotImplemented
}

func (f *fakeQueries) MarkDelivered(context.Context, dbgen.MarkDeliveredParams) error {
	return errNotImplemented
}

func (f *fakeQueries) MarkFailedWithBackoff(context.Context, dbgen.MarkFailedWithBackoffParams) error {
	return errNotImplemented
}

func (f *fakeQueries) MoveToDLQ(context.Context, dbgen.MoveToDLQParams) error {
	return errNotImplemented
}

func (f *fakeQueries) ResetDeliveryForReplay(context.Context, pgtype.UUID) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, errNotImplemented
}

func (f *fakeQueries) EnqueueDelivery(context.Context, dbgen.EnqueueDeliveryParams) (dbgen.WebhookDelivery, error) {
	return dbgen.WebhookDelivery{}, errNotImplemented
}

func (f *fakeQueries) GetWebhookEndpoint(context.Context, pgtype.UUID) (dbgen.WebhookEndpoint, error) {
	return dbgen.WebhookEndpoint{}, errNotImplemented
}
