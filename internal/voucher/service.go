package voucher

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Querier captures the database methods required by the voucher service.
type Querier interface {
	GetVoucherByCodeForUpdate(ctx context.Context, code string) (dbgen.Voucher, error)
	CountVoucherUsageByUser(ctx context.Context, arg dbgen.CountVoucherUsageByUserParams) (int64, error)
	GetVoucherUsageByOrder(ctx context.Context, arg dbgen.GetVoucherUsageByOrderParams) (dbgen.VoucherUsage, error)
	InsertVoucherUsage(ctx context.Context, arg dbgen.InsertVoucherUsageParams) error
	IncreaseVoucherUsedCount(ctx context.Context, id pgtype.UUID) error
}

// PreviewResult describes the outcome of evaluating a voucher without mutating state.
type PreviewResult struct {
	Discount       int64  `json:"discount"`
	EligibleAmount int64  `json:"eligible_amount"`
	Code           string `json:"code"`
}

// Service encapsulates voucher rules evaluation and settlement behaviour.
type Service struct {
	Q                   Querier
	Now                 func() time.Time
	DefaultPerUserLimit int
}

// Preview performs a dry-run evaluation for the given cart context.
func (s *Service) Preview(ctx context.Context, code string, userID *string, cartTotal int64, items []Item) (PreviewResult, error) {
	if s == nil || s.Q == nil {
		return PreviewResult{}, errors.New("voucher service not configured")
	}
	trimmed := strings.TrimSpace(code)
	if trimmed == "" {
		return PreviewResult{}, fmt.Errorf("code is required: %w", ErrNotEligible)
	}
	voucher, err := s.Q.GetVoucherByCodeForUpdate(ctx, trimmed)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PreviewResult{}, ErrNotEligible
		}
		return PreviewResult{}, err
	}
	rule := RuleFromModel(voucher)
	rule.MaxStack = 1
	rule.DefaultLimit = s.DefaultPerUserLimit

	limit := effectivePerUserLimit(rule)
	if userID != nil && *userID != "" && limit > 0 {
		userUUID, err := parseUUID(*userID)
		if err != nil {
			return PreviewResult{}, fmt.Errorf("invalid user id: %w", err)
		}
		used, err := s.Q.CountVoucherUsageByUser(ctx, dbgen.CountVoucherUsageByUserParams{VoucherID: voucher.ID, UserID: userUUID})
		if err != nil {
			return PreviewResult{}, err
		}
		rule.PerUserUsed = int32(used)
		rule.EffectiveLimit = limit
	} else if limit > 0 {
		rule.EffectiveLimit = limit
	}

	if err := rule.Validate(s.now(), cartTotal); err != nil {
		return PreviewResult{}, err
	}
	eligible := EligibleSubtotal(items, rule)
	if eligible <= 0 {
		return PreviewResult{}, ErrNotEligible
	}
	discount := Compute(eligible, rule)
	if discount <= 0 {
		return PreviewResult{}, ErrNotEligible
	}
	return PreviewResult{Discount: discount, EligibleAmount: eligible, Code: voucher.Code}, nil
}

// Settle records voucher usage at order payment time ensuring idempotency.
func (s *Service) Settle(ctx context.Context, code string, orderID pgtype.UUID, userID pgtype.UUID, amount int64) error {
	if s == nil || s.Q == nil {
		return errors.New("voucher service not configured")
	}
	if strings.TrimSpace(code) == "" || !orderID.Valid || amount < 0 {
		return nil
	}
	voucher, err := s.Q.GetVoucherByCodeForUpdate(ctx, code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if amount < 0 {
		amount = 0
	}
	_, err = s.Q.GetVoucherUsageByOrder(ctx, dbgen.GetVoucherUsageByOrderParams{VoucherID: voucher.ID, OrderID: orderID})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	params := dbgen.InsertVoucherUsageParams{VoucherID: voucher.ID, OrderID: orderID, Amount: amount}
	if userID.Valid {
		params.UserID = userID
	}
	if err := s.Q.InsertVoucherUsage(ctx, params); err != nil {
		return err
	}
	_ = s.Q.IncreaseVoucherUsedCount(ctx, voucher.ID)
	return nil
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// RuleFromModel converts the generated sqlc model into a Rule used for evaluation.
func RuleFromModel(v dbgen.Voucher) Rule {
	rule := Rule{
		Code:         v.Code,
		Kind:         string(v.Kind),
		Value:        v.Value,
		MinSpend:     v.MinSpend,
		UsedCount:    v.UsedCount,
		PerUserLimit: nullableInt32(v.PerUserLimit),
		PercentBps:   nullableInt32(v.PercentBps),
		Combinable:   v.Combinable,
		Priority:     int(v.Priority),
	}
	if v.ValidFrom.Valid {
		rule.ValidFrom = &v.ValidFrom.Time
	}
	if v.ValidTo.Valid {
		rule.ValidTo = &v.ValidTo.Time
	}
	if v.UsageLimit.Valid {
		limit := v.UsageLimit.Int32
		rule.UsageLimit = &limit
	}
	rule.ProductIDs = toUUIDSlice(v.ProductIds)
	rule.CategoryIDs = toUUIDSlice(v.CategoryIds)
	rule.BrandIDs = toUUIDSlice(v.BrandIds)
	return rule
}

func effectivePerUserLimit(rule Rule) int32 {
	if rule.PerUserLimit != nil && *rule.PerUserLimit > 0 {
		return *rule.PerUserLimit
	}
	if rule.DefaultLimit > 0 {
		return int32(rule.DefaultLimit)
	}
	return 0
}

func nullableInt32(v pgtype.Int4) *int32 {
	if v.Valid {
		val := v.Int32
		return &val
	}
	return nil
}

func toUUIDSlice(values []pgtype.UUID) []uuid.UUID {
	if len(values) == 0 {
		return nil
	}
	out := make([]uuid.UUID, 0, len(values))
	for _, v := range values {
		if v.Valid {
			out = append(out, uuid.UUID(v.Bytes))
		}
	}
	return out
}

func parseUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
