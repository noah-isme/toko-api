package catalog

import (
	"context"
	"reflect"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// Change sources reported in product.* event payloads.
const (
	ChangeSourceSingle = "single"
	ChangeSourceBulk   = "bulk"
	ChangeSourceImport = "import"
)

// Product change fields. Each maps to one product.* topic.
const (
	FieldPrice     = "price"
	FieldCompareAt = "compare_at"
	FieldStock     = "stock"
	FieldStatus    = "status"
)

// ProductChange describes one changed catalog value. VariantID/SKU are set for
// variant-level changes (variant price, stock).
type ProductChange struct {
	ProductID pgtype.UUID
	VariantID pgtype.UUID
	Slug      string
	SKU       string
	Field     string
	Old       any
	New       any
}

// ProductEventEmitter is the subset of events.Bus used to publish changes.
type ProductEventEmitter interface {
	EmitForTenant(ctx context.Context, tenantID pgtype.UUID, topic string, aggregateID pgtype.UUID, payload any) (dbgen.DomainEvent, error)
}

// TopicForField returns the product.* topic for a change field ("" if none).
func TopicForField(field string) string {
	switch field {
	case FieldPrice, FieldCompareAt:
		return events.TopicProductPriceChanged
	case FieldStock:
		return events.TopicProductStockChanged
	case FieldStatus:
		return events.TopicProductStatusChanged
	}
	return ""
}

// EmitProductChanges publishes one product.* domain event per change whose old
// and new values differ. Events are stamped with tenantID so webhook deliveries
// stay inside the tenant; without a valid tenant nothing is emitted (a product
// event must never become a NULL-tenant broadcast). It runs on a context that
// ignores the request's cancellation because the database change has already
// been committed; failures are logged and never returned. It returns the
// number of events emitted.
func EmitProductChanges(ctx context.Context, emitter ProductEventEmitter, tenantID pgtype.UUID, source string, changes []ProductChange) int {
	if emitter == nil || len(changes) == 0 {
		return 0
	}
	logger := zerolog.Ctx(ctx)
	if !tenantID.Valid {
		logger.Warn().Int("changes", len(changes)).Msg("skip product events: no tenant in context")
		return 0
	}
	emitCtx := context.WithoutCancel(ctx)
	requestID := middleware.GetReqID(ctx)
	actorUserID, _ := common.UserID(ctx)
	tenantStr := uuidStr(tenantID)
	emitted := 0
	for _, change := range changes {
		topic := TopicForField(change.Field)
		if topic == "" || !change.ProductID.Valid || reflect.DeepEqual(change.Old, change.New) {
			continue
		}
		payload := map[string]any{
			"tenantId":  tenantStr,
			"productId": uuidStr(change.ProductID),
			"field":     change.Field,
			"old":       change.Old,
			"new":       change.New,
			"source":    source,
			"requestId": requestID,
		}
		if change.Slug != "" {
			payload["slug"] = change.Slug
		}
		if change.VariantID.Valid {
			payload["variantId"] = uuidStr(change.VariantID)
		}
		if change.SKU != "" {
			payload["sku"] = change.SKU
		}
		if actorUserID != "" {
			payload["actorUserId"] = actorUserID
		}
		if _, err := emitter.EmitForTenant(emitCtx, tenantID, topic, change.ProductID, payload); err != nil {
			logger.Error().Err(err).Str("topic", topic).Str("product_id", uuidStr(change.ProductID)).Msg("emit product event")
			continue
		}
		emitted++
	}
	return emitted
}

func uuidStr(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
