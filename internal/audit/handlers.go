package audit

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Handler exposes HTTP endpoints for working with audit logs.
type Handler struct {
	Store Store
}

// logDTO is the camelCase view of an audit log row. The generated sqlc model is
// not serialised directly because its pgtype fields and []byte metadata do not
// map onto the JSON contract the admin dashboard consumes.
type logDTO struct {
	ID           string         `json:"id"`
	ActorKind    string         `json:"actorKind"`
	UserID       *string        `json:"userId"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resourceType"`
	ResourceID   *string        `json:"resourceId"`
	Method       string         `json:"method"`
	Path         string         `json:"path"`
	Route        *string        `json:"route"`
	Status       int            `json:"status"`
	IPAddress    *string        `json:"ipAddress"`
	UserAgent    *string        `json:"userAgent"`
	RequestID    *string        `json:"requestId"`
	Metadata     map[string]any `json:"metadata"`
	CreatedAt    string         `json:"createdAt"`
}

// List returns a paginated, filterable list of audit logs for administrators.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "AUDIT_NOT_CONFIGURED", "audit store not configured", nil)
		return
	}

	query := r.URL.Query()
	page, limit := common.ParsePagination(r, 50)
	if limit > 200 {
		limit = 200
	}
	// `offset` is still honoured for callers that page without `page`.
	offset := (page - 1) * limit
	if raw := strings.TrimSpace(query.Get("offset")); raw != "" {
		if parsed := common.AtoiDefault(raw, offset); parsed >= 0 {
			offset = parsed
		}
	}

	actorUserID, ok := parseUUIDFilter(query.Get("userId"))
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "INVALID_USER_ID", "userId must be a UUID", nil)
		return
	}
	startDate, ok := parseTimeFilter(query.Get("startDate"))
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "INVALID_START_DATE", "startDate must be an RFC3339 timestamp", nil)
		return
	}
	endDate, ok := parseTimeFilter(query.Get("endDate"))
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "INVALID_END_DATE", "endDate must be an RFC3339 timestamp", nil)
		return
	}

	filters := dbgen.ListAuditLogsFilteredParams{
		ActorUserID:  actorUserID,
		Action:       textFilter(query.Get("action")),
		ResourceType: textFilter(query.Get("resourceType")),
		StartDate:    startDate,
		EndDate:      endDate,
		Limit:        int32(limit),
		Offset:       int32(offset),
	}

	rows, err := h.Store.ListAuditLogsFiltered(r.Context(), filters)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "AUDIT_QUERY_FAILED", "unable to fetch audit logs", nil)
		return
	}
	total, err := h.Store.CountAuditLogsFiltered(r.Context(), dbgen.CountAuditLogsFilteredParams{
		ActorUserID:  filters.ActorUserID,
		Action:       filters.Action,
		ResourceType: filters.ResourceType,
		StartDate:    filters.StartDate,
		EndDate:      filters.EndDate,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "AUDIT_COUNT_FAILED", "unable to count audit logs", nil)
		return
	}

	items := make([]logDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, toDTO(row))
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"data": items,
		"pagination": common.Pagination{
			Page:       page,
			PerPage:    limit,
			TotalItems: int(total),
		},
	})
}

func toDTO(row dbgen.AuditLog) logDTO {
	dto := logDTO{
		ActorKind:    string(row.ActorKind),
		Action:       row.Action,
		ResourceType: row.ResourceType,
		Method:       row.Method,
		Path:         row.Path,
		Status:       int(row.Status),
		Metadata:     decodeMetadata(row.Metadata),
	}
	if row.ID.Valid {
		dto.ID = uuid.UUID(row.ID.Bytes).String()
	}
	if row.ActorUserID.Valid {
		id := uuid.UUID(row.ActorUserID.Bytes).String()
		dto.UserID = &id
	}
	dto.ResourceID = textPtr(row.ResourceID)
	dto.Route = textPtr(row.Route)
	dto.IPAddress = textPtr(row.Ip)
	dto.UserAgent = textPtr(row.UserAgent)
	dto.RequestID = textPtr(row.RequestID)
	if row.CreatedAt.Valid {
		dto.CreatedAt = row.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	return dto
}

// decodeMetadata turns the raw jsonb column into a map so it serialises as an
// object rather than a base64 string.
func decodeMetadata(raw []byte) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func textPtr(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	v := value.String
	return &v
}

func textFilter(value string) pgtype.Text {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: trimmed, Valid: true}
}

func parseUUIDFilter(value string) (pgtype.UUID, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.UUID{}, true
	}
	parsed, err := uuid.Parse(trimmed)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, true
}

func parseTimeFilter(value string) (pgtype.Timestamptz, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return pgtype.Timestamptz{}, true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02"} {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return pgtype.Timestamptz{Time: parsed, Valid: true}, true
		}
	}
	return pgtype.Timestamptz{}, false
}
