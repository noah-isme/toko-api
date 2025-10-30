package repo

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/tenant"
)

var (
	// ErrTenantMissing indicates the tenant identifier was not found in context.
	ErrTenantMissing = errors.New("tenant missing")
	// ErrTenantInvalid indicates the tenant identifier could not be parsed.
	ErrTenantInvalid = errors.New("tenant invalid")
)

func tenantUUIDFromContext(ctx context.Context) (pgtype.UUID, error) {
	tenantID, ok := tenant.From(ctx)
	if !ok {
		return pgtype.UUID{}, ErrTenantMissing
	}
	tid, err := uuidValue(tenantID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("%w: %v", ErrTenantInvalid, err)
	}
	return tid, nil
}

func uuidValue(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, err
	}
	var tid pgtype.UUID
	tid.Bytes = parsed
	tid.Valid = true
	return tid, nil
}
