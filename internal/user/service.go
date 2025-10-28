package user

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/common"
	db "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	httpStatusBadRequest   = 400
	httpStatusUnauthorized = 401
	httpStatusNotFound     = 404
)

// Address represents a user address in API-friendly format.
type Address struct {
	ID           string    `json:"id"`
	Label        string    `json:"label,omitempty"`
	ReceiverName string    `json:"receiver_name,omitempty"`
	Phone        string    `json:"phone,omitempty"`
	Country      string    `json:"country,omitempty"`
	Province     string    `json:"province,omitempty"`
	City         string    `json:"city,omitempty"`
	PostalCode   string    `json:"postal_code,omitempty"`
	AddressLine1 string    `json:"address_line1,omitempty"`
	AddressLine2 string    `json:"address_line2,omitempty"`
	IsDefault    bool      `json:"is_default"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// AddressInput captures payload for creating or updating an address.
type AddressInput struct {
	Label        string
	ReceiverName string
	Phone        string
	Country      string
	Province     string
	City         string
	PostalCode   string
	AddressLine1 string
	AddressLine2 string
	IsDefault    bool
}

// Service orchestrates address book operations.
type Service struct {
	pool    *pgxpool.Pool
	queries *db.Queries
}

// NewService constructs a new address service.
func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, queries: db.New(pool)}
}

// List returns paginated addresses for a user.
func (s *Service) List(ctx context.Context, userID string, page, perPage int) ([]Address, int64, error) {
	uid, err := toUUID(userID)
	if err != nil {
		return nil, 0, common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	if perPage <= 0 {
		perPage = 20
	}
	if page <= 0 {
		page = 1
	}
	offset := int32((page - 1) * perPage)
	limit := int32(perPage)

	rows, err := s.queries.ListAddressesByUser(ctx, db.ListAddressesByUserParams{UserID: uid, Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.queries.CountAddressesByUser(ctx, uid)
	if err != nil {
		return nil, 0, err
	}
	addresses := make([]Address, 0, len(rows))
	for _, row := range rows {
		addresses = append(addresses, convertAddress(row))
	}
	return addresses, total, nil
}

// Create inserts a new address for the given user.
func (s *Service) Create(ctx context.Context, userID string, input AddressInput) (Address, error) {
	uid, err := toUUID(userID)
	if err != nil {
		return Address{}, common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	if strings.TrimSpace(input.AddressLine1) == "" {
		return Address{}, common.NewAppError("VALIDATION_ERROR", "address_line1 is required", httpStatusBadRequest, nil)
	}
	if strings.TrimSpace(input.ReceiverName) == "" {
		return Address{}, common.NewAppError("VALIDATION_ERROR", "receiver_name is required", httpStatusBadRequest, nil)
	}
	if strings.TrimSpace(input.Phone) == "" {
		return Address{}, common.NewAppError("VALIDATION_ERROR", "phone is required", httpStatusBadRequest, nil)
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Address{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := s.queries.WithTx(tx)
	if input.IsDefault {
		if err := qtx.UnsetDefaultAddresses(ctx, db.UnsetDefaultAddressesParams{UserID: uid}); err != nil {
			return Address{}, err
		}
	}

	created, err := qtx.CreateAddress(ctx, db.CreateAddressParams{
		UserID:       uid,
		Label:        toText(input.Label),
		ReceiverName: toText(input.ReceiverName),
		Phone:        toText(input.Phone),
		Country:      toText(input.Country),
		Province:     toText(input.Province),
		City:         toText(input.City),
		PostalCode:   toText(input.PostalCode),
		AddressLine1: toText(input.AddressLine1),
		AddressLine2: toText(input.AddressLine2),
		IsDefault:    input.IsDefault,
	})
	if err != nil {
		return Address{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Address{}, err
	}
	return convertAddress(created), nil
}

// Update modifies an existing address.
func (s *Service) Update(ctx context.Context, userID, addressID string, input AddressInput) (Address, error) {
	uid, err := toUUID(userID)
	if err != nil {
		return Address{}, common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	aid, err := toUUID(addressID)
	if err != nil {
		return Address{}, common.NewAppError("NOT_FOUND", "address not found", httpStatusNotFound, nil)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Address{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := s.queries.WithTx(tx)
	if _, err := qtx.GetAddressByID(ctx, db.GetAddressByIDParams{ID: aid, UserID: uid}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Address{}, common.NewAppError("NOT_FOUND", "address not found", httpStatusNotFound, nil)
		}
		return Address{}, err
	}

	if input.IsDefault {
		if err := qtx.UnsetDefaultAddresses(ctx, db.UnsetDefaultAddressesParams{UserID: uid, ExcludeID: aid}); err != nil {
			return Address{}, err
		}
	}

	updated, err := qtx.UpdateAddress(ctx, db.UpdateAddressParams{
		ID:           aid,
		UserID:       uid,
		Label:        toText(input.Label),
		ReceiverName: toText(input.ReceiverName),
		Phone:        toText(input.Phone),
		Country:      toText(input.Country),
		Province:     toText(input.Province),
		City:         toText(input.City),
		PostalCode:   toText(input.PostalCode),
		AddressLine1: toText(input.AddressLine1),
		AddressLine2: toText(input.AddressLine2),
		IsDefault:    input.IsDefault,
	})
	if err != nil {
		return Address{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Address{}, err
	}
	return convertAddress(updated), nil
}

// Delete removes an address.
func (s *Service) Delete(ctx context.Context, userID, addressID string) error {
	uid, err := toUUID(userID)
	if err != nil {
		return common.NewAppError("UNAUTHORIZED", "unauthorized", httpStatusUnauthorized, nil)
	}
	aid, err := toUUID(addressID)
	if err != nil {
		return common.NewAppError("NOT_FOUND", "address not found", httpStatusNotFound, nil)
	}
	if _, err := s.queries.GetAddressByID(ctx, db.GetAddressByIDParams{ID: aid, UserID: uid}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return common.NewAppError("NOT_FOUND", "address not found", httpStatusNotFound, nil)
		}
		return err
	}
	if err := s.queries.DeleteAddress(ctx, db.DeleteAddressParams{ID: aid, UserID: uid}); err != nil {
		return err
	}
	return nil
}

func convertAddress(row db.Address) Address {
	return Address{
		ID:           uuidString(row.ID),
		Label:        textToString(row.Label),
		ReceiverName: textToString(row.ReceiverName),
		Phone:        textToString(row.Phone),
		Country:      textToString(row.Country),
		Province:     textToString(row.Province),
		City:         textToString(row.City),
		PostalCode:   textToString(row.PostalCode),
		AddressLine1: textToString(row.AddressLine1),
		AddressLine2: textToString(row.AddressLine2),
		IsDefault:    row.IsDefault,
		CreatedAt:    timeFromPG(row.CreatedAt),
		UpdatedAt:    timeFromPG(row.UpdatedAt),
	}
}

func toUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	u, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return ""
	}
	return u.String()
}

func toText(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: trimmed, Valid: true}
}

func textToString(value pgtype.Text) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func timeFromPG(ts pgtype.Timestamptz) time.Time {
	if !ts.Valid {
		return time.Time{}
	}
	return ts.Time
}
