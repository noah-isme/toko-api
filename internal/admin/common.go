// Package admin implements the administrative catalog, order, and voucher
// read/write endpoints backing the admin dashboard. Public catalog reads live in
// internal/catalog; this package intentionally bypasses that cache-aware path
// because admins need uncached, management-shaped payloads.
package admin

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

const (
	defaultLimit = 20
	maxLimit     = 100
)

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	parsed, err := uuid.FromBytes(id.Bytes[:])
	if err != nil {
		return ""
	}
	return parsed.String()
}

func parsePGUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func optionalUUID(value *string) (pgtype.UUID, error) {
	if value == nil {
		return pgtype.UUID{}, nil
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return pgtype.UUID{}, nil
	}
	return parsePGUUID(trimmed)
}

func text(value string) pgtype.Text {
	if strings.TrimSpace(value) == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func optionalText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: trimmed, Valid: true}
}

func optionalInt64(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}

func optionalInt32(value *int32) pgtype.Int4 {
	if value == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *value, Valid: true}
}

func optionalBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}

func nullableString(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	out := v.String
	return &out
}

func nullableInt64(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	out := v.Int64
	return &out
}

func nullableInt32(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	out := v.Int32
	return &out
}

func nullableTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	out := v.Time
	return &out
}

func nullableUUID(v pgtype.UUID) *string {
	if !v.Valid {
		return nil
	}
	out := uuidString(v)
	if out == "" {
		return nil
	}
	return &out
}

func timestamp(v pgtype.Timestamptz) time.Time {
	if !v.Valid {
		return time.Time{}
	}
	return v.Time
}

func uuidStrings(ids []pgtype.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if value := uuidString(id); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func formatNullableTime(v pgtype.Timestamptz) *string {
	t := nullableTime(v)
	if t == nil {
		return nil
	}
	formatted := t.UTC().Format(timeLayout)
	return &formatted
}

// parseListQuery reads page/limit from the request, clamping to safe bounds.
func parseListQuery(r *http.Request) (page, limit, offset int) {
	page = 1
	limit = defaultLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("page")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			page = parsed
		}
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	offset = (page - 1) * limit
	return page, limit, offset
}

func queryText(r *http.Request, key string) pgtype.Text {
	return text(strings.TrimSpace(r.URL.Query().Get(key)))
}

func queryBool(r *http.Request, key string) pgtype.Bool {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return pgtype.Bool{}
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: parsed, Valid: true}
}

func queryTime(r *http.Request, key string) (pgtype.Timestamptz, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return pgtype.Timestamptz{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return pgtype.Timestamptz{Time: parsed, Valid: true}, nil
		}
	}
	return pgtype.Timestamptz{}, errors.New("invalid date: " + key)
}

func writePaginated(w http.ResponseWriter, data any, page, limit int, total int64) {
	common.JSON(w, http.StatusOK, map[string]any{
		"data":       data,
		"pagination": common.Pagination{Page: page, PerPage: limit, TotalItems: int(total)},
	})
}

func slugify(value string) string {
	lower := strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range lower {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// isUniqueViolation reports whether err is a Postgres unique constraint failure.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 23505") || strings.Contains(err.Error(), "23505")
}

// isForeignKeyViolation reports whether err is a Postgres FK constraint failure.
func isForeignKeyViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE 23503") || strings.Contains(err.Error(), "23503")
}

func orderStatusFilter(raw string) (dbgen.NullOrderStatus, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(raw))
	if trimmed == "" || trimmed == "ALL" {
		return dbgen.NullOrderStatus{}, nil
	}
	status := dbgen.OrderStatus(trimmed)
	switch status {
	case dbgen.OrderStatusPENDINGPAYMENT,
		dbgen.OrderStatusPAID,
		dbgen.OrderStatusPACKED,
		dbgen.OrderStatusSHIPPED,
		dbgen.OrderStatusOUTFORDELIVERY,
		dbgen.OrderStatusDELIVERED,
		dbgen.OrderStatusCANCELLED:
		return dbgen.NullOrderStatus{OrderStatus: status, Valid: true}, nil
	}
	return dbgen.NullOrderStatus{}, errors.New("invalid status")
}

func discountKindFilter(raw string) (dbgen.NullDiscountKind, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" || trimmed == "all" {
		return dbgen.NullDiscountKind{}, nil
	}
	// Accept the UI-facing aliases as well as the DB enum values.
	switch trimmed {
	case "percentage", "percent":
		return dbgen.NullDiscountKind{DiscountKind: dbgen.DiscountKindPercent, Valid: true}, nil
	case "fixed", "fixed_amount":
		return dbgen.NullDiscountKind{DiscountKind: dbgen.DiscountKindFixedAmount, Valid: true}, nil
	}
	return dbgen.NullDiscountKind{}, errors.New("invalid kind")
}
