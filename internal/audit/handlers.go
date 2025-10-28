package audit

import (
	"net/http"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Handler exposes HTTP endpoints for working with audit logs.
type Handler struct {
	Store Store
}

// List returns a paginated list of audit logs for administrators.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "AUDIT_NOT_CONFIGURED", "audit store not configured", nil)
		return
	}
	limit := common.AtoiDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset := common.AtoiDefault(r.URL.Query().Get("offset"), 0)
	if offset < 0 {
		offset = 0
	}

	rows, err := h.Store.ListAuditLogs(r.Context(), dbgen.ListAuditLogsParams{Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "AUDIT_QUERY_FAILED", "unable to fetch audit logs", nil)
		return
	}
	common.JSON(w, http.StatusOK, rows)
}
