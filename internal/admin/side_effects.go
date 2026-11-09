package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/noah-isme/backend-toko/internal/audit"
	"github.com/noah-isme/backend-toko/internal/catalog"
	"github.com/noah-isme/backend-toko/internal/common"
)

// TxBeginner starts database transactions (*pgxpool.Pool satisfies it).
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// afterProductChanges applies the side effects every catalog write shares,
// whatever its path (single update, bulk, import): it invalidates the cached
// product detail/list for each touched slug and emits one product.* domain event
// per changed value, stamped with the request's tenant. It must run after the
// database change is committed. Slugs lists products touched without an
// event-worthy change (e.g. a title edit) that still need cache invalidation.
func (h *CatalogHandler) afterProductChanges(ctx context.Context, source string, changes []catalog.ProductChange, slugs ...string) {
	if h == nil {
		return
	}
	cacheCtx := context.WithoutCancel(ctx)
	seen := make(map[string]struct{}, len(changes)+len(slugs))
	invalidate := func(slug string) {
		if slug == "" {
			return
		}
		if _, ok := seen[slug]; ok {
			return
		}
		seen[slug] = struct{}{}
		h.invalidate(cacheCtx, slug)
	}
	for _, change := range changes {
		invalidate(change.Slug)
	}
	for _, slug := range slugs {
		invalidate(slug)
	}
	if len(seen) == 0 && h.Cache != nil {
		h.Cache.InvalidateList(cacheCtx)
	}
	// Stock and price writes (single, bulk and import) may re-arm product alerts.
	h.kickAlerts()
	if h.Events == nil {
		return
	}
	catalog.EmitProductChanges(ctx, h.Events, tenantIDFromContext(ctx), source, changes)
}

// recordCatalogAudit writes an explicit audit row for a catalog operation with
// per-item from/to values. Callers pass the transaction's store so the row
// commits (or rolls back) together with the change. The request id is the one
// chi's RequestID middleware assigned (it reuses a client X-Request-ID), so the
// row correlates with the product.* events' requestId (and with the group-level
// audit middleware row when the client sent X-Request-ID).
func (h *CatalogHandler) recordCatalogAudit(ctx context.Context, store audit.Store, r *http.Request, action, resourceType string, status int, metadata any) error {
	if h == nil || !h.AuditEnabled || store == nil {
		return nil
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	req := r.Clone(ctx)
	if requestID := middleware.GetReqID(r.Context()); requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	actor := audit.Actor{Kind: audit.ActorKindAnonymous}
	if userID, ok := common.UserID(r.Context()); ok && userID != "" {
		actor = audit.Actor{Kind: audit.ActorKindUser, UserID: &userID}
	}
	// SamplingRate 0: explicit catalog audit rows are never sampled away.
	svc := audit.Service{Store: store, Enabled: true}
	return svc.Record(ctx, actor, action, resourceType, "", req, status, data)
}

// isLockConflict reports lock-timeout (55P03), deadlock (40P01) and
// serialization (40001) failures, which bulk and import map to 409 CONFLICT.
func isLockConflict(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "55P03", "40P01", "40001":
			return true
		}
	}
	return false
}

// beginCatalogTx starts a transaction with a bounded lock wait so a bulk or
// import request never stalls checkout for longer than 5 seconds.
func beginCatalogTx(ctx context.Context, pool TxBeginner) (pgx.Tx, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL lock_timeout = '5s'"); err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}

// writeTxError maps a transaction failure to the bulk/import error contract.
func writeTxError(w http.ResponseWriter, err error, what string) {
	switch {
	case isLockConflict(err):
		common.JSONError(w, http.StatusConflict, "CONFLICT", "concurrent catalog change, retry", nil)
	case isUniqueViolation(err):
		common.JSONError(w, http.StatusConflict, "CONFLICT", "concurrent catalog change (unique value taken), retry", nil)
	default:
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", what, nil)
	}
}
